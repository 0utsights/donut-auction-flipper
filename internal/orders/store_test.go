package orders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"donut-network/internal/market"
)

func TestSQLiteBackupProducesStandaloneDatabase(t *testing.T) {
	path := t.TempDir() + "/market.db"
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	backup, err := store.Backup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if backup == "" {
		t.Fatal("backup path is empty")
	}
	if info, err := os.Stat(backup); err != nil || info.Size() == 0 {
		t.Fatalf("backup missing: info=%v err=%v", info, err)
	}
}

func TestSQLiteBackupCoalescesRestartBackupsPerUTCDay(t *testing.T) {
	path := t.TempDir() + "/market.db"
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	firstDay := time.Date(2026, 8, 26, 1, 2, 3, 0, time.UTC)
	store.now = func() time.Time { return firstDay }
	first, err := store.Backup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return firstDay.Add(10 * time.Hour) }
	second, err := store.Backup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("same-day restart created another backup: first=%q second=%q", first, second)
	}
	store.now = func() time.Time { return firstDay.Add(24 * time.Hour) }
	third, err := store.Backup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if third == first {
		t.Fatal("next UTC day did not create a new backup")
	}
	entries, err := os.ReadDir(path + ".backups")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("backup entries=%d, want one per UTC day", len(entries))
	}
}

func TestBackupPruningPreservesManualSafetyCopies(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	for _, name := range []string{
		"20260825T010000Z.db", "20260825T230000Z.db", "20260826T010000Z.db", "pre-migration.db",
	} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := pruneAutomaticBackups(directory, entries, now); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"20260825T230000Z.db", "20260826T010000Z.db", "pre-migration.db"} {
		if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
			t.Fatalf("expected retained backup %q: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(directory, "20260825T010000Z.db")); !os.IsNotExist(err) {
		t.Fatalf("duplicate automatic backup was not pruned: %v", err)
	}
}

func TestCleanupRetainsOnlyOneDayOfRawScans(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	for _, value := range []struct {
		observer string
		seen     time.Time
	}{{"expired", now.Add(-25 * time.Hour)}, {"current", now.Add(-23 * time.Hour)}} {
		if _, err := store.db.Exec(`INSERT INTO scans(observer_id,task_id,session_id,content_hash,screen_title,page,complete,unknown_schema,schema_reason,observed_ms,received_ms)
			VALUES(?,'',?,?,'Orders (Page 1)',1,1,0,'',?,?)`, value.observer, value.observer, value.observer, value.seen.UnixMilli(), value.seen.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	var expired, current int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM scans WHERE observer_id='expired'`).Scan(&expired); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM scans WHERE observer_id='current'`).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if expired != 0 || current != 1 {
		t.Fatalf("raw scan retention expired=%d current=%d", expired, current)
	}
}

func TestCleanupCascadesExpiredScansAcrossBatches(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	for index := 0; index < cleanupBatchSize+1; index++ {
		result, err := store.db.Exec(`INSERT INTO scans(observer_id,task_id,session_id,content_hash,screen_title,page,complete,unknown_schema,schema_reason,observed_ms,received_ms)
			VALUES('expired','',?,?,'Orders (Page 1)',1,1,0,'',?,?)`, fmt.Sprintf("session-%d", index), fmt.Sprintf("hash-%d", index), now.Add(-25*time.Hour).UnixMilli(), now.Add(-25*time.Hour).UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		scanID, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`INSERT INTO order_rows(scan_id,observer_id,order_key,item_id,signature,display_name,quantity,max_stack_size,unit_reward,unit_reward_cents,requested_quantity,remaining_quantity,owner,expires_ms,price_position,slot,raw_field_hash,signature_complete,identity_verified,observed_ms)
			VALUES(?,'expired',?,'minecraft:stone','minecraft:stone','Stone',1,64,1,100,1,1,'',0,1,0,?,1,1,?)`, scanID, fmt.Sprintf("order-%d", index), fmt.Sprintf("raw-%d", index), now.Add(-25*time.Hour).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"scans", "order_rows"} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("expired %s rows remain: %d", table, count)
		}
	}
}

func TestCleanupPrunesExpiredCompactPriceSamples(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	for _, value := range []struct {
		session string
		seen    time.Time
	}{{"expired", now.Add(-25 * time.Hour)}, {"current", now.Add(-23 * time.Hour)}} {
		if _, err := store.db.Exec(`INSERT INTO order_price_samples(signature,observer_id,session_id,unit_reward_cents,price_position,observed_ms)
			VALUES('minecraft:stone','observer',?,100,1,?)`, value.session, value.seen.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM order_price_samples`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("retained compact price samples=%d, want 1", count)
	}
}

func TestCleanupPrunesExpiredCompactSignatureSamples(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	for _, value := range []struct {
		observer string
		seen     time.Time
	}{{"expired", now.Add(-25 * time.Hour)}, {"current", now.Add(-23 * time.Hour)}} {
		if _, err := store.db.Exec(`INSERT INTO order_signature_samples(signature,observer_id,parser_version,signature_complete,observed_ms)
			VALUES('minecraft:stone',?,'p1',1,?)`, value.observer, value.seen.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM order_signature_samples`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("retained compact signature samples=%d, want 1", count)
	}
}

func TestStoreIndexesFreshnessWindows(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for table, name := range map[string]string{
		"order_rows":              "order_rows_observed_time",
		"fill_events":             "fill_observed_time",
		"order_signature_samples": "order_signature_samples_recent",
	} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM pragma_index_list(?) WHERE name=?`, table, name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("freshness index %s is missing from %s", name, table)
		}
	}
}

func TestLatestObserverClassificationSupersedesOlderRowsButPreservesConsensus(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 8, 27, 20, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	if _, err := store.db.Exec(`INSERT INTO order_evidence_summary(signature,item_id,display_name,complete_scans,first_seen_ms,last_seen_ms,observed_quantity,max_stack_size,available_units)
		VALUES('minecraft:stone','minecraft:stone','Stone',3,?,?,64,64,64)`, now.Add(-time.Minute).UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	for _, observer := range []string{"one", "two"} {
		if _, err := store.db.Exec(`INSERT INTO observers(observer_id,parser_version,proxy_label,state,last_seen_ms) VALUES(?,'p1','test','online',?)`, observer, now.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(observer string, complete bool, observed time.Time) {
		t.Helper()
		result, err := store.db.Exec(`INSERT INTO scans(observer_id,task_id,session_id,content_hash,screen_title,page,complete,unknown_schema,schema_reason,observed_ms,received_ms)
			VALUES(?,'',?,?, 'Orders (Page 1)',1,1,0,'',?,?)`, observer, observer+observed.String(), observer+observed.String(), observed.UnixMilli(), observed.UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		scanID, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`INSERT INTO order_rows(scan_id,observer_id,order_key,item_id,signature,display_name,quantity,max_stack_size,unit_reward,unit_reward_cents,requested_quantity,remaining_quantity,owner,expires_ms,price_position,slot,raw_field_hash,signature_complete,parser_version,identity_verified,observed_ms)
			VALUES(?,?,?,'minecraft:stone','minecraft:stone','Stone',64,64,0,100,64,64,'',0,1,0,?,?,'p1',1,?)`,
			scanID, observer, observer+observed.String(), observer+observed.String(), boolInt(complete), observed.UnixMilli()); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`INSERT INTO order_signature_samples(signature,observer_id,parser_version,signature_complete,observed_ms)
			VALUES('minecraft:stone',?,'p1',?,?) ON CONFLICT(signature,observer_id) DO UPDATE SET
			parser_version=excluded.parser_version,signature_complete=excluded.signature_complete,observed_ms=excluded.observed_ms`,
			observer, boolInt(complete), observed.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	insert("one", false, now.Add(-time.Minute))
	insert("one", true, now.Add(-30*time.Second))
	insert("two", true, now.Add(-20*time.Second))
	evidence, err := store.Evidence(context.Background())
	if err != nil || len(evidence) != 1 || !evidence[0].SignatureComplete {
		t.Fatalf("fresh observer classifications did not replace an old incomplete row: evidence=%+v err=%v", evidence, err)
	}
	insert("two", false, now.Add(-10*time.Second))
	evidence, err = store.Evidence(context.Background())
	if err != nil || len(evidence) != 1 || evidence[0].SignatureComplete {
		t.Fatalf("latest observer disagreement did not fail closed: evidence=%+v err=%v", evidence, err)
	}
	if _, err := store.db.Exec(`UPDATE observers SET parser_version='p2'`); err != nil {
		t.Fatal(err)
	}
	evidence, err = store.Evidence(context.Background())
	if err != nil || len(evidence) != 1 || evidence[0].SignatureComplete {
		t.Fatalf("old parser version retained completeness: evidence=%+v err=%v", evidence, err)
	}
}

func TestEmptyCandidateFeedSerializesAsArray(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	feed := system.CandidateFeed()
	if feed.Candidates == nil {
		t.Fatal("empty candidate feed is nil")
	}
	encoded, err := json.Marshal(feed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"candidates":[]`) {
		t.Fatalf("empty candidate feed is not an array: %s", encoded)
	}
}

func TestObserverLeaseAndIdempotentScan(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	if _, err := system.Register(ctx, ObserverRegistration{ObserverID: "one", ParserVersion: "p1", ProxyLabel: "proxy"}); err != nil {
		t.Fatal(err)
	}
	task, err := system.LeaseTask(ctx, "one")
	if err != nil || task == nil || task.Kind != "discovery" {
		t.Fatalf("task=%+v err=%v", task, err)
	}
	if task.LeaseToken == "" {
		t.Fatal("leased task has no ownership token")
	}
	batch := scan("one", task.ID, "session-1", 0, time.Now().UTC(), order("order-1", 100))
	batch.LeaseToken = task.LeaseToken
	wrongLease := batch
	wrongLease.SessionID = "wrong-lease"
	wrongLease.ContentHash = strings.Repeat("b", 64)
	wrongLease.LeaseToken = "lease_wrong"
	if _, err := system.SaveScan(ctx, wrongLease); err == nil {
		t.Fatal("scan without the current lease token was accepted")
	}
	inserted, err := system.SaveScan(ctx, batch)
	if err != nil || !inserted {
		t.Fatalf("inserted=%v err=%v", inserted, err)
	}
	inserted, err = system.SaveScan(ctx, batch)
	if err != nil || inserted {
		t.Fatalf("duplicate inserted=%v err=%v", inserted, err)
	}
}

func TestScanCompletenessFailsClosedWhenSameSignatureRowsDisagree(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	if _, err := system.Register(ctx, ObserverRegistration{ObserverID: "one", ParserVersion: "p1", ProxyLabel: "proxy"}); err != nil {
		t.Fatal(err)
	}
	task, err := system.LeaseTask(ctx, "one")
	if err != nil || task == nil {
		t.Fatalf("task=%+v err=%v", task, err)
	}
	complete := order("order-complete", 100)
	incomplete := order("order-incomplete", 100)
	incomplete.SignatureComplete = false
	batch := scan("one", task.ID, "mixed-completeness", 0, time.Now().UTC(), complete, incomplete)
	batch.LeaseToken = task.LeaseToken
	if inserted, err := system.SaveScan(ctx, batch); err != nil || !inserted {
		t.Fatalf("inserted=%v err=%v", inserted, err)
	}
	var compactComplete bool
	if err := system.store.db.QueryRow(`SELECT signature_complete FROM order_signature_samples
		WHERE signature='minecraft:diamond' AND observer_id='one'`).Scan(&compactComplete); err != nil {
		t.Fatal(err)
	}
	if compactComplete {
		t.Fatal("mixed canonical classifications were promoted to complete")
	}
}

func TestFocusedWatchRequestsCurrentDiscoveryLeaseToYield(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	if _, err := system.Register(ctx, ObserverRegistration{ObserverID: "one", ParserVersion: "p1", ProxyLabel: "proxy"}); err != nil {
		t.Fatal(err)
	}
	discovery, err := system.LeaseTask(ctx, "one")
	if err != nil || discovery == nil || discovery.Kind != "discovery" {
		t.Fatalf("discovery=%+v err=%v", discovery, err)
	}
	heartbeat := Heartbeat{ObserverID: "one", State: "scanning", TaskID: discovery.ID, LeaseToken: discovery.LeaseToken, Page: 3}
	if yield, err := system.ShouldYieldDiscovery(ctx, heartbeat); err != nil || yield {
		t.Fatalf("discovery yielded without focused work: yield=%v err=%v", yield, err)
	}
	watch, err := system.AddWatch(ctx, "minecraft:diamond")
	if err != nil {
		t.Fatal(err)
	}
	if watch.ExpiresAt.Sub(watch.CreatedAt) != time.Minute {
		t.Fatalf("watch lifetime=%s", watch.ExpiresAt.Sub(watch.CreatedAt))
	}
	if yield, err := system.ShouldYieldDiscovery(ctx, heartbeat); err != nil || !yield {
		t.Fatalf("focused work did not preempt discovery: yield=%v err=%v", yield, err)
	}
	wrong := heartbeat
	wrong.LeaseToken = "lease_wrong"
	if yield, err := system.ShouldYieldDiscovery(ctx, wrong); err != nil || yield {
		t.Fatalf("wrong lease interrupted discovery: yield=%v err=%v", yield, err)
	}
	if err := system.CompleteTask(ctx, TaskResult{ObserverID: "one", TaskID: discovery.ID, LeaseToken: discovery.LeaseToken, Status: "complete"}); err != nil {
		t.Fatal(err)
	}
	focused, err := system.LeaseTask(ctx, "one")
	if err != nil || focused == nil || focused.Kind != "focused_watch" {
		t.Fatalf("focused=%+v err=%v", focused, err)
	}
}

func TestAutomaticResearchYieldsAfterTopPage(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	if _, err := system.Register(ctx, ObserverRegistration{ObserverID: "one", ParserVersion: "p1", ProxyLabel: "proxy"}); err != nil {
		t.Fatal(err)
	}
	discovery, err := system.LeaseTask(ctx, "one")
	if err != nil || discovery == nil || discovery.Kind != "discovery" {
		t.Fatalf("discovery=%+v err=%v", discovery, err)
	}
	if err := system.store.QueueAutomaticResearch(ctx, []string{"minecraft:diamond_block"}, nil, time.Minute, 5*time.Minute, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	heartbeat := Heartbeat{ObserverID: "one", State: "scanning", TaskID: discovery.ID, LeaseToken: discovery.LeaseToken, Page: automaticFocusDiscoveryPage}
	if yield, err := system.ShouldYieldDiscovery(ctx, heartbeat); err != nil || !yield {
		t.Fatalf("automatic research did not yield at breadth floor: yield=%v err=%v", yield, err)
	}
	wrong := heartbeat
	wrong.LeaseToken = "lease_wrong"
	if yield, err := system.ShouldYieldDiscovery(ctx, wrong); err != nil || yield {
		t.Fatalf("wrong lease interrupted discovery: yield=%v err=%v", yield, err)
	}
}

func TestProfileRevalidationYieldsAfterTopPage(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	if _, err = system.Register(ctx, ObserverRegistration{ObserverID: "one", ParserVersion: "p1", ProxyLabel: "proxy"}); err != nil {
		t.Fatal(err)
	}
	discovery, err := system.LeaseTask(ctx, "one")
	if err != nil || discovery == nil || discovery.Kind != "discovery" {
		t.Fatalf("discovery=%+v err=%v", discovery, err)
	}
	priorities := map[string]int{"minecraft:diamond_block": 75}
	if err = system.store.QueueAutomaticResearch(ctx, []string{"minecraft:diamond_block"}, priorities, time.Minute, 5*time.Minute, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	heartbeat := Heartbeat{ObserverID: "one", State: "scanning", TaskID: discovery.ID, LeaseToken: discovery.LeaseToken, Page: automaticFocusDiscoveryPage}
	if yield, yieldErr := system.ShouldYieldDiscovery(ctx, heartbeat); yieldErr != nil || !yield {
		t.Fatalf("profile revalidation did not run after the breadth floor: yield=%v err=%v", yield, yieldErr)
	}
}

func TestAutomaticResearchDoesNotWatchAuctionOnlyDeficiency(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	value := Candidate{Route: "ORDER_TO_AUCTION", State: "RESEARCH", OrderTier: "actionable",
		Signature: "minecraft:breeze_rod", SignatureComplete: true, PriorityRank: 1, PriorityScore: 100,
		TargetListPrice: 10_000, ConservativeProfit: 1_000}
	system.candidates.Store(&CandidateFeed{Candidates: []Candidate{value}})
	if err := system.queueAutomaticResearch(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := system.store.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE kind='focused_watch'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("auction-only confidence deficiency consumed Mineflayer focused-watch time")
	}
	value.OrderTier = "research"
	system.candidates.Store(&CandidateFeed{Candidates: []Candidate{value}})
	if err := system.queueAutomaticResearch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := system.store.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE kind='focused_watch'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("order-evidence deficiency did not queue focused research: count=%d err=%v", count, err)
	}
}

func TestPreviouslyProvenStaleCandidateQueuesExpeditedRevalidation(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	system.candidates.Store(&CandidateFeed{Candidates: []Candidate{{
		Route: "ORDER_TO_AUCTION", State: "STALE", OrderTier: "actionable", Profiled: true,
		Signature: "minecraft:diamond_block", SignatureComplete: true, PriorityScore: 100,
		TargetListPrice: 100_000, ConservativeProfit: 10_000,
	}}})
	if err = system.queueAutomaticResearch(context.Background()); err != nil {
		t.Fatal(err)
	}
	var priority int
	if err = system.store.db.QueryRow(`SELECT priority FROM tasks WHERE kind='focused_watch'`).Scan(&priority); err != nil {
		t.Fatal(err)
	}
	if priority != 75 {
		t.Fatalf("profile revalidation priority=%d", priority)
	}
}

func TestLongLivedProfileCanBeScheduledWithoutACurrentCandidate(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	if _, err = system.store.db.ExecContext(ctx, `INSERT INTO order_market_profiles(signature,fill_events,distinct_orders,last_fill_ms)
		VALUES('minecraft:diamond_block',?,?,?)`, profileMinFillEvents, profileMinDistinctOrders, time.Now().UTC().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err = system.queueAutomaticResearch(ctx); err != nil {
		t.Fatal(err)
	}
	var signature string
	var priority int
	if err = system.store.db.QueryRow(`SELECT signature,priority FROM tasks WHERE kind='focused_watch'`).Scan(&signature, &priority); err != nil {
		t.Fatal(err)
	}
	if signature != "minecraft:diamond_block" || priority != 75 {
		t.Fatalf("profile task=%s priority=%d", signature, priority)
	}
}

func TestLegacyDollarRewardsAreQuarantinedFromCentEvidence(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	now := time.Now().UTC()
	result, err := system.store.db.ExecContext(ctx, `INSERT INTO scans(observer_id,session_id,content_hash,screen_title,page,complete,observed_ms,received_ms)
		VALUES('legacy','legacy-session','legacy-hash','Orders (Page 1)',1,1,?,?)`, now.Add(-time.Minute).UnixMilli(), now.Add(-time.Minute).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	scanID, _ := result.LastInsertId()
	_, err = system.store.db.ExecContext(ctx, `INSERT INTO order_rows(scan_id,observer_id,order_key,item_id,signature,display_name,quantity,max_stack_size,unit_reward,unit_reward_cents,requested_quantity,remaining_quantity,slot,raw_field_hash,signature_complete,observed_ms)
		VALUES(?,'legacy','legacy-order','minecraft:diamond','minecraft:diamond','Diamond',1,64,5000,0,100,100,0,?,1,?)`, scanID, strings.Repeat("b", 64), now.Add(-time.Minute).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	_, err = system.store.db.ExecContext(ctx, `INSERT INTO fill_events(signature,order_key,observer_id,units,unit_reward,unit_reward_cents,observed_ms)
		VALUES('minecraft:diamond','legacy-order','legacy',99,5000,0,?)`, now.Add(-time.Minute).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := system.store.Evidence(ctx)
	if err != nil || len(evidence) != 0 {
		t.Fatalf("legacy whole-dollar row entered cent evidence: evidence=%+v err=%v", evidence, err)
	}
	if _, err := system.Register(ctx, ObserverRegistration{ObserverID: "current", ParserVersion: "p1", ProxyLabel: "proxy"}); err != nil {
		t.Fatal(err)
	}
	if _, err := system.SaveScan(ctx, scan("current", "", "cent-session", 1, now, order("current-order", 100))); err != nil {
		t.Fatal(err)
	}
	evidence, err = system.store.Evidence(ctx)
	if err != nil || len(evidence) != 1 || evidence[0].BestUnitRewardCents != 100 || evidence[0].FillEvents != 0 || evidence[0].FilledUnits24h != 0 {
		t.Fatalf("exact-cent observation missing: evidence=%+v err=%v", evidence, err)
	}
}

func TestObservationWithoutCompetitiveBucketIsDiagnosticOnly(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	if _, err = system.Register(ctx, ObserverRegistration{ObserverID: "current", ParserVersion: "p1", ProxyLabel: "proxy"}); err != nil {
		t.Fatal(err)
	}
	value := order("unsafe", 100)
	value.CompetitiveUnitRewardCents = value.UnitRewardCents
	if _, err = system.SaveScan(ctx, scan("current", "", "missing-boundary", 1, time.Now().UTC(), value)); err != nil {
		t.Fatal(err)
	}
	evidence, err := system.store.Evidence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 0 {
		t.Fatalf("boundary-less observation entered economics: %+v", evidence)
	}
	var rawRows int
	if err = system.store.db.QueryRow(`SELECT COUNT(*) FROM order_rows WHERE order_key='unsafe'`).Scan(&rawRows); err != nil || rawRows != 1 {
		t.Fatalf("diagnostic raw row missing: count=%d err=%v", rawRows, err)
	}
}

func TestDiscoveryTaskCadenceAndSchemaHold(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	now := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	system.store.now = func() time.Time { return now }
	if _, err := system.Register(ctx, ObserverRegistration{ObserverID: "one", ParserVersion: "p1", ProxyLabel: "proxy"}); err != nil {
		t.Fatal(err)
	}

	task, err := system.LeaseTask(ctx, "one")
	if err != nil || task == nil {
		t.Fatalf("initial task=%+v err=%v", task, err)
	}
	if err := system.CompleteTask(ctx, TaskResult{ObserverID: "one", TaskID: task.ID, LeaseToken: task.LeaseToken, Status: "complete"}); err != nil {
		t.Fatal(err)
	}
	if err := system.Heartbeat(ctx, Heartbeat{ObserverID: "one", State: "scanning", TaskID: task.ID, LeaseToken: task.LeaseToken, Page: 1}); err != nil {
		t.Fatalf("late heartbeat for completed task was not idempotent: %v", err)
	}
	observer, err := system.store.observer(ctx, "one")
	if err != nil || observer.CurrentTaskID != "" || observer.CurrentPage != 0 {
		t.Fatalf("late heartbeat left stale observer task: observer=%+v err=%v", observer, err)
	}
	if immediate, err := system.LeaseTask(ctx, "one"); err != nil || immediate != nil {
		t.Fatalf("discovery task ignored freshness: task=%+v err=%v", immediate, err)
	}
	now = now.Add(5 * time.Second)
	task, err = system.LeaseTask(ctx, "one")
	if err != nil || task == nil {
		t.Fatalf("discovery task was not released after freshness interval: task=%+v err=%v", task, err)
	}
	if err := system.CompleteTask(ctx, TaskResult{ObserverID: "one", TaskID: task.ID, LeaseToken: task.LeaseToken, Status: "failed", Message: "unknown schema"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if held, err := system.LeaseTask(ctx, "one"); err != nil || held != nil {
		t.Fatalf("schema-held discovery task was re-leased: task=%+v err=%v", held, err)
	}
}

func TestDiscoveryCompletionQueuesHighestPriorityResearch(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	if _, err = system.Register(ctx, ObserverRegistration{ObserverID: "one", ParserVersion: "p1", ProxyLabel: "proxy"}); err != nil {
		t.Fatal(err)
	}
	task, err := system.LeaseTask(ctx, "one")
	if err != nil || task == nil || task.Kind != "discovery" {
		t.Fatalf("discovery=%+v err=%v", task, err)
	}
	system.candidates.Store(&CandidateFeed{Candidates: []Candidate{
		{Route: "ORDER_TO_AUCTION", State: "RESEARCH", Signature: "minecraft:iron_ingot", SignatureComplete: true, PriorityRank: 1, PriorityScore: 100, TargetListPrice: 100_000, ConservativeProfit: 10_000},
		{Route: "ORDER_TO_AUCTION", State: "RESEARCH", Signature: "minecraft:diamond_block", SignatureComplete: true, PriorityRank: 2, PriorityScore: 90, TargetListPrice: 5_000_000, ConservativeProfit: 9_000},
	}})
	if err = system.CompleteTask(ctx, TaskResult{ObserverID: "one", TaskID: task.ID, LeaseToken: task.LeaseToken, Status: "complete"}); err != nil {
		t.Fatal(err)
	}
	focused, err := system.LeaseTask(ctx, "one")
	if err != nil || focused == nil {
		t.Fatalf("automatic focused task=%+v err=%v", focused, err)
	}
	if focused.Kind != "focused_watch" || focused.Signature != "minecraft:iron_ingot" || focused.Priority != 50 {
		t.Fatalf("wrong automatic priority task: %+v", focused)
	}
	if err = system.CompleteTask(ctx, TaskResult{ObserverID: "one", TaskID: focused.ID, LeaseToken: focused.LeaseToken, Status: "complete"}); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = system.store.db.QueryRowContext(ctx, `SELECT state FROM tasks WHERE id=?`, focused.ID).Scan(&state); err != nil || state != "completed" {
		t.Fatalf("one-shot task state=%q err=%v", state, err)
	}
}

func TestManualWatchUpgradesAutomaticResearchTask(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	if err = system.store.QueueAutomaticResearch(ctx, []string{"minecraft:diamond_block"}, nil, time.Minute, 5*time.Minute, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err = system.AddWatch(ctx, "minecraft:diamond_block"); err != nil {
		t.Fatal(err)
	}
	var priority, automatic int
	if err = system.store.db.QueryRowContext(ctx, `SELECT priority,automatic FROM tasks WHERE kind='focused_watch' AND state='ready'`).Scan(&priority, &automatic); err != nil {
		t.Fatal(err)
	}
	if priority != 100 || automatic != 0 {
		t.Fatalf("manual watch did not upgrade task: priority=%d automatic=%d", priority, automatic)
	}
}

func TestObserverHealthBecomesOfflineAfterMissedHeartbeats(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	now := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	system.store.now = func() time.Time { return now }
	if _, err := system.Register(ctx, ObserverRegistration{ObserverID: "one", ParserVersion: "p1", ProxyLabel: "proxy"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(16 * time.Second)
	observers, err := system.store.Observers(ctx)
	if err != nil || len(observers) != 1 || observers[0].State != "offline" {
		t.Fatalf("stale observer health=%+v err=%v", observers, err)
	}
}

func TestExistingObserverLeaseIsRenewedOnResume(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	now := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	system.store.now = func() time.Time { return now }
	if _, err := system.Register(ctx, ObserverRegistration{ObserverID: "one", ParserVersion: "p1", ProxyLabel: "proxy"}); err != nil {
		t.Fatal(err)
	}
	first, err := system.LeaseTask(ctx, "one")
	if err != nil || first == nil {
		t.Fatalf("initial task=%+v err=%v", first, err)
	}
	now = now.Add(29 * time.Second)
	resumed, err := system.LeaseTask(ctx, "one")
	if err != nil || resumed == nil || resumed.ID != first.ID || resumed.LeaseToken != first.LeaseToken {
		t.Fatalf("resumed task=%+v err=%v", resumed, err)
	}
	if want := now.Add(30 * time.Second); !resumed.LeaseExpiresAt.Equal(want) {
		t.Fatalf("lease expires %s, want %s", resumed.LeaseExpiresAt, want)
	}
	now = now.Add(2 * time.Second)
	if err := system.Heartbeat(ctx, Heartbeat{ObserverID: "one", State: "scanning", TaskID: resumed.ID, LeaseToken: "lease_wrong"}); err == nil {
		t.Fatal("wrong token for active lease was accepted")
	}
	if err := system.Heartbeat(ctx, Heartbeat{ObserverID: "one", State: "scanning", TaskID: resumed.ID, LeaseToken: resumed.LeaseToken}); err != nil {
		t.Fatalf("renewed lease was already invalid: %v", err)
	}
}

func TestUnknownOrIncompleteScanCannotCreateEconomicEvidence(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	now := time.Now().UTC()
	system.store.now = func() time.Time { return now }
	_, _ = system.Register(ctx, ObserverRegistration{ObserverID: "one", ParserVersion: "p1", ProxyLabel: "proxy"})
	old := scan("one", "", "old-capture", 999, now.Add(-16*time.Minute), order("old-unverified", 100))
	old.Complete = false
	old.UnknownSchema = true
	old.SchemaReason = "old fixture unknown"
	if inserted, err := system.SaveScan(ctx, old); err != nil || !inserted {
		t.Fatalf("old capture insert=%v err=%v", inserted, err)
	}
	batch := scan("one", "", "capture", 0, now, order("unverified", 100))
	batch.Complete = false
	batch.UnknownSchema = true
	batch.SchemaReason = "fixture unknown"
	if inserted, err := system.SaveScan(ctx, batch); err != nil || !inserted {
		t.Fatalf("capture insert=%v err=%v", inserted, err)
	}
	evidence, err := system.store.Evidence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 0 {
		t.Fatalf("capture-only rows entered valuation evidence: %+v", evidence)
	}
	debug, err := system.store.Debug(ctx)
	if err != nil || debug.ScanCoverage.UnknownSchema != 1 || debug.ScanCoverage.Incomplete != 1 {
		t.Fatalf("capture coverage=%+v err=%v", debug.ScanCoverage, err)
	}
}

func TestDiagnosticBatchIsAtomicAndDebugShowsNewestSafeEvents(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	now := time.Date(2026, 8, 30, 18, 0, 0, 0, time.UTC)
	system.store.now = func() time.Time { return now }
	initial := make([]Diagnostic, 499)
	for index := range initial {
		initial[index] = Diagnostic{InstallID: "install-one", Version: "alpha.38", Event: "decision",
			Code: "initial", Fields: map[string]string{"reason_code": "test"}, CreatedAt: now}
	}
	if err := system.SaveDiagnostics(ctx, initial); err != nil {
		t.Fatal(err)
	}
	rejected := []Diagnostic{
		{InstallID: "install-one", Version: "alpha.38", Event: "error", Code: "must-not-commit", CreatedAt: now},
		{InstallID: "install-one", Version: "alpha.38", Event: "error", Code: "must-not-commit", CreatedAt: now},
	}
	if err := system.SaveDiagnostics(ctx, rejected); !errors.Is(err, ErrDiagnosticRateLimit) {
		t.Fatalf("rate limit error=%v", err)
	}
	var count int
	if err := system.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM diagnostics`).Scan(&count); err != nil || count != 499 {
		t.Fatalf("partially committed rejected batch count=%d err=%v", count, err)
	}
	now = now.Add(time.Hour + time.Second)
	newest := Diagnostic{InstallID: "install-one", Version: "alpha.38", Event: "decision", Code: "focused_stale",
		Fields: map[string]string{"model_version": "42", "reason_code": "focused_stale"}, CreatedAt: now}
	if err := system.SaveDiagnostic(ctx, newest); err != nil {
		t.Fatal(err)
	}
	debug, err := system.store.Debug(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(debug.RecentDiagnostics) != 50 || debug.RecentDiagnostics[0].Code != "focused_stale" ||
		debug.RecentDiagnostics[0].Fields["model_version"] != "42" {
		t.Fatalf("recent diagnostics=%+v", debug.RecentDiagnostics)
	}
}

func TestSharedFocusedWatchDoesNotDuplicateOrPrematurelyStopTask(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	first, err := system.AddWatch(ctx, "minecraft:diamond")
	if err != nil {
		t.Fatal(err)
	}
	second, err := system.AddWatch(ctx, "minecraft:diamond")
	if err != nil {
		t.Fatal(err)
	}
	var active int
	if err := system.store.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE kind='focused_watch' AND state IN ('ready','leased')`).Scan(&active); err != nil || active != 1 {
		t.Fatalf("active focused tasks=%d err=%v", active, err)
	}
	if err := system.DeleteWatch(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := system.store.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE kind='focused_watch' AND state IN ('ready','leased')`).Scan(&active); err != nil || active != 1 {
		t.Fatalf("remaining watch lost task: active=%d err=%v", active, err)
	}
	if err := system.DeleteWatch(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	if err := system.store.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE kind='focused_watch' AND state IN ('ready','leased')`).Scan(&active); err != nil || active != 0 {
		t.Fatalf("orphan focused task remains: active=%d err=%v", active, err)
	}
}

func TestBoundedFabricWatchUsesRequestedLifetime(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	now := time.Date(2026, 8, 30, 16, 0, 0, 0, time.UTC)
	system.store.now = func() time.Time { return now }
	watch, err := system.AddWatchFor(context.Background(), "minecraft:spider_eye", 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !watch.CreatedAt.Equal(now) || !watch.ExpiresAt.Equal(now.Add(15*time.Second)) {
		t.Fatalf("bounded watch=%+v", watch)
	}
}

func TestDeletingWatchLetsLeasedTaskFinish(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	if _, err = system.Register(ctx, ObserverRegistration{ObserverID: "observer", ParserVersion: "p1", ProxyLabel: "proxy"}); err != nil {
		t.Fatal(err)
	}
	watch, err := system.AddWatch(ctx, "minecraft:mace")
	if err != nil {
		t.Fatal(err)
	}
	task, err := system.LeaseTask(ctx, "observer")
	if err != nil || task == nil || task.Kind != "focused_watch" {
		t.Fatalf("focused lease=%+v err=%v", task, err)
	}
	if err = system.DeleteWatch(ctx, watch.ID); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = system.store.db.QueryRow(`SELECT state FROM tasks WHERE id=?`, task.ID).Scan(&state); err != nil || state != "leased" {
		t.Fatalf("active watch was invalidated during deletion: state=%q err=%v", state, err)
	}
	if err = system.CompleteTask(ctx, TaskResult{ObserverID: "observer", TaskID: task.ID, LeaseToken: task.LeaseToken, Status: "complete"}); err != nil {
		t.Fatalf("finishing deleted watch: %v", err)
	}
	if err = system.store.db.QueryRow(`SELECT state FROM tasks WHERE id=?`, task.ID).Scan(&state); err != nil || state != "completed" {
		t.Fatalf("finished watch state=%q err=%v", state, err)
	}
}

func TestNoActiveOrdersDoesNotMonopolizeObserver(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	if _, err = system.Register(ctx, ObserverRegistration{ObserverID: "observer", ParserVersion: "p1", ProxyLabel: "proxy"}); err != nil {
		t.Fatal(err)
	}
	if _, err = system.AddWatch(ctx, "minecraft:diamond_block"); err != nil {
		t.Fatal(err)
	}
	task, err := system.LeaseTask(ctx, "observer")
	if err != nil || task == nil || task.Kind != "focused_watch" {
		t.Fatalf("focused lease=%+v err=%v", task, err)
	}
	if err = system.CompleteTask(ctx, TaskResult{ObserverID: "observer", TaskID: task.ID, LeaseToken: task.LeaseToken, Status: "complete", Message: "no_active_orders"}); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = system.store.db.QueryRow(`SELECT state FROM tasks WHERE id=?`, task.ID).Scan(&state); err != nil || state != "completed" {
		t.Fatalf("off-page watch remained active: state=%q err=%v", state, err)
	}
}

func TestCurrentPriceAndQueueDoNotUseHistoricalBest(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	now := time.Now().UTC()
	system.store.now = func() time.Time { return now }
	_, _ = system.Register(ctx, ObserverRegistration{ObserverID: "one", ParserVersion: "p1", ProxyLabel: "proxy"})
	historical := order("same", 100)
	historical.UnitRewardCents = 10_000
	historical.CompetitiveUnitRewardCents = 10_001
	historical.PricePosition = 1
	if _, err := system.SaveScan(ctx, scan("one", "", "old", 1, now.Add(-9*time.Minute), historical)); err != nil {
		t.Fatal(err)
	}
	for index, age := range []time.Duration{90 * time.Second, 45 * time.Second, 0} {
		current := order("same", 100)
		current.UnitRewardCents = 100
		current.CompetitiveUnitRewardCents = 101
		current.PricePosition = 3
		if _, err := system.SaveScan(ctx, scan("one", "", fmt.Sprintf("current-%d", index), index+2, now.Add(-age), current)); err != nil {
			t.Fatal(err)
		}
	}
	evidence, err := system.store.Evidence(ctx)
	if err != nil || len(evidence) != 1 {
		t.Fatalf("evidence=%+v err=%v", evidence, err)
	}
	if evidence[0].BestUnitRewardCents != 100 || evidence[0].BestPricePosition != 1 || !evidence[0].LastSeenAt.Equal(now.Truncate(time.Millisecond)) {
		t.Fatalf("historical price leaked into current evidence: %+v", evidence[0])
	}
}

func TestPriceStabilityUsesTopOfBookPerSessionNotPaginationDepth(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	_, _ = system.Register(ctx, ObserverRegistration{ObserverID: "one", ParserVersion: "p1", ProxyLabel: "proxy"})
	base := time.Now().UTC().Add(-40 * time.Second)
	for pass := 0; pass < 3; pass++ {
		for page, reward := range []int64{100, 90, 80} {
			value := order(fmt.Sprintf("order-%d-%d", pass, page), 100)
			value.UnitRewardCents = reward
			value.CompetitiveUnitRewardCents = reward + 1
			batch := scan("one", "", fmt.Sprintf("session-%d", pass), page+1, base.Add(time.Duration(pass*15)*time.Second), value)
			batch.ContentHash = fmt.Sprintf("%064x", 1_000+pass*10+page)
			if _, err := system.SaveScan(ctx, batch); err != nil {
				t.Fatal(err)
			}
		}
	}
	evidence, err := system.store.Evidence(ctx)
	if err != nil || len(evidence) != 1 {
		t.Fatalf("evidence=%+v err=%v", evidence, err)
	}
	if evidence[0].CompleteScans != 3 || evidence[0].BestUnitRewardCents != 100 || !evidence[0].Stable {
		t.Fatalf("pagination tiers corrupted top-of-book stability: %+v", evidence[0])
	}
}

func TestFocusedSampleTracksLatestPriceInsteadOfSessionMaximum(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	_, _ = system.Register(ctx, ObserverRegistration{ObserverID: "one", ParserVersion: "p1", ProxyLabel: "proxy"})
	if _, err := system.AddWatch(ctx, "minecraft:diamond"); err != nil {
		t.Fatal(err)
	}
	task, err := system.LeaseTask(ctx, "one")
	if err != nil || task == nil || task.Kind != "focused_watch" {
		t.Fatalf("focused task=%+v err=%v", task, err)
	}
	now := time.Now().UTC()
	for index, reward := range []int64{100, 80} {
		value := order("same", 100)
		value.UnitRewardCents = reward
		value.CompetitiveUnitRewardCents = reward + 1
		batch := scan("one", task.ID, "one-focused-session", 1, now.Add(time.Duration(index)*time.Second), value)
		batch.LeaseToken = task.LeaseToken
		batch.ContentHash = fmt.Sprintf("%064x", 9_000+index)
		if _, err := system.SaveScan(ctx, batch); err != nil {
			t.Fatal(err)
		}
	}
	evidence, err := system.store.Evidence(ctx)
	if err != nil || len(evidence) != 1 {
		t.Fatalf("evidence=%+v err=%v", evidence, err)
	}
	if evidence[0].BestUnitRewardCents != 80 || evidence[0].FocusedUnitRewardCents != 80 ||
		!evidence[0].FocusedSeenAt.Equal(now.Add(time.Second).Truncate(time.Millisecond)) {
		t.Fatalf("focused session retained a stale peak price: %+v", evidence[0])
	}
}

func TestFillInferenceRequiresObservedQuantityDecrease(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	_, _ = system.Register(ctx, ObserverRegistration{ObserverID: "one", ParserVersion: "p1", ProxyLabel: "proxy"})
	if _, err := system.AddWatch(ctx, "minecraft:diamond"); err != nil {
		t.Fatal(err)
	}
	task, err := system.LeaseTask(ctx, "one")
	if err != nil || task == nil || task.Kind != "focused_watch" {
		t.Fatalf("focused task=%+v err=%v", task, err)
	}
	base := time.Now().UTC().Add(-20 * time.Minute)
	observedAt := []time.Duration{0, 10 * time.Minute, 18 * time.Minute, 18*time.Minute + 45*time.Second, 19*time.Minute + 15*time.Second, 20 * time.Minute}
	orders := []OrderObservation{order("a", 100), order("b", 100), order("c", 100)}
	for index := 0; index < 6; index++ {
		for orderIndex := range orders {
			if index > 0 && orderIndex <= index%3 {
				orders[orderIndex].RemainingQuantity -= 1
			}
		}
		batch := scan("one", "", fmt.Sprintf("steady-session-%d", index), 1, base.Add(observedAt[index]), orders...)
		batch.ContentHash = fmt.Sprintf("%064x", index+100)
		batch.TaskID, batch.LeaseToken = task.ID, task.LeaseToken
		if _, err := system.SaveScan(ctx, batch); err != nil {
			t.Fatal(err)
		}
	}
	evidence, err := system.store.Evidence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 1 {
		t.Fatalf("evidence=%+v", evidence)
	}
	if evidence[0].FillEvents != 6 || evidence[0].DistinctOrders != 3 || evidence[0].Tier != "actionable" ||
		!evidence[0].Profiled || evidence[0].ProfileFillEvents != 6 || evidence[0].ProfileDistinctOrders != 3 {
		t.Fatalf("expected actionable fill evidence, got %+v", evidence[0])
	}
	if evidence[0].FocusedSeenAt.IsZero() || !evidence[0].FocusedSeenAt.Equal(base.Add(observedAt[5]).Truncate(time.Millisecond)) {
		t.Fatalf("focused freshness was not recorded independently: %+v", evidence[0])
	}
	var confirmed, quarantined int
	if err := system.store.db.QueryRow(`SELECT SUM(CASE WHEN confirmation_level>=2 THEN 1 ELSE 0 END),SUM(CASE WHEN confirmation_level<2 THEN 1 ELSE 0 END) FROM fill_events`).Scan(&confirmed, &quarantined); err != nil || confirmed != 6 || quarantined != 5 {
		t.Fatalf("fill confirmation split confirmed=%d quarantined=%d err=%v", confirmed, quarantined, err)
	}
	// A missing row is deliberately ambiguous and does not create a fill event.
	before := evidence[0].FillEvents
	if _, err := system.SaveScan(ctx, scan("one", "", "empty-session", 9, time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	evidence, _ = system.store.Evidence(ctx)
	if evidence[0].FillEvents != before {
		t.Fatal("disappearing order was guessed as a fill")
	}
}

func TestCrossPagePseudoIdentityCannotConfirmFill(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	_, _ = system.Register(ctx, ObserverRegistration{ObserverID: "one", ParserVersion: "p1", ProxyLabel: "proxy"})
	if _, err := system.AddWatch(ctx, "minecraft:diamond"); err != nil {
		t.Fatal(err)
	}
	task, err := system.LeaseTask(ctx, "one")
	if err != nil || task == nil || task.Kind != "focused_watch" {
		t.Fatalf("focused task=%+v err=%v", task, err)
	}
	now := time.Now().UTC()
	first := scan("one", "", "same-session", 1, now, order("pseudo", 100))
	second := scan("one", "", "same-session", 2, now.Add(time.Second), order("pseudo", 50))
	first.TaskID, first.LeaseToken = task.ID, task.LeaseToken
	second.TaskID, second.LeaseToken = task.ID, task.LeaseToken
	if _, err := system.SaveScan(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := system.SaveScan(ctx, second); err != nil {
		t.Fatal(err)
	}
	evidence, err := system.store.Evidence(ctx)
	if err != nil || len(evidence) != 1 {
		t.Fatalf("evidence=%+v err=%v", evidence, err)
	}
	if evidence[0].FillEvents != 0 || evidence[0].FilledUnits24h != 0 {
		t.Fatalf("cross-page collision became confirmed fill: %+v", evidence[0])
	}
}

func TestDiscoveryScanCannotConfirmFill(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	_, _ = system.Register(ctx, ObserverRegistration{ObserverID: "one", ParserVersion: "p1", ProxyLabel: "proxy"})
	task, err := system.LeaseTask(ctx, "one")
	if err != nil || task == nil || task.Kind != "discovery" {
		t.Fatalf("discovery task=%+v err=%v", task, err)
	}
	now := time.Now().UTC()
	first := scan("one", task.ID, "session", 1, now, order("pseudo", 100))
	second := scan("one", task.ID, "session", 1, now.Add(time.Second), order("pseudo", 50))
	first.LeaseToken, second.LeaseToken = task.LeaseToken, task.LeaseToken
	second.ContentHash = strings.Repeat("c", 64)
	if _, err := system.SaveScan(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := system.SaveScan(ctx, second); err != nil {
		t.Fatal(err)
	}
	evidence, err := system.store.Evidence(ctx)
	if err != nil || len(evidence) != 1 || evidence[0].FillEvents != 0 {
		t.Fatalf("discovery reduction became trusted evidence: evidence=%+v err=%v", evidence, err)
	}
	if !evidence[0].FocusedSeenAt.IsZero() {
		t.Fatalf("discovery page was mislabeled as focused freshness: %+v", evidence[0])
	}
}

func TestFocusedWatchCannotConfirmNeighborItemFill(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	_, _ = system.Register(ctx, ObserverRegistration{ObserverID: "one", ParserVersion: "p1", ProxyLabel: "proxy"})
	if _, err := system.AddWatch(ctx, "minecraft:diamond"); err != nil {
		t.Fatal(err)
	}
	task, err := system.LeaseTask(ctx, "one")
	if err != nil || task == nil || task.Kind != "focused_watch" {
		t.Fatalf("focused task=%+v err=%v", task, err)
	}
	now := time.Now().UTC()
	firstOrder := order("neighbor", 100)
	firstOrder.ItemID, firstOrder.Signature, firstOrder.DisplayName = "minecraft:dirt", "minecraft:dirt", "Dirt"
	secondOrder := firstOrder
	secondOrder.RemainingQuantity = 50
	first := scan("one", task.ID, "first", 1, now, firstOrder)
	second := scan("one", task.ID, "second", 1, now.Add(time.Second), secondOrder)
	first.LeaseToken, second.LeaseToken = task.LeaseToken, task.LeaseToken
	second.ContentHash = strings.Repeat("d", 64)
	if _, err := system.SaveScan(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := system.SaveScan(ctx, second); err != nil {
		t.Fatal(err)
	}
	evidence, err := system.store.Evidence(ctx)
	if err != nil || len(evidence) != 0 {
		t.Fatalf("neighbor item entered focused evidence: evidence=%+v err=%v", evidence, err)
	}
}

func TestFocusedWatchConfirmsBoundedSyntheticOrderIdentity(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	_, _ = system.Register(ctx, ObserverRegistration{ObserverID: "one", ParserVersion: "p1", ProxyLabel: "proxy"})
	if _, err := system.AddWatch(ctx, "minecraft:diamond"); err != nil {
		t.Fatal(err)
	}
	task, err := system.LeaseTask(ctx, "one")
	if err != nil || task == nil || task.Kind != "focused_watch" {
		t.Fatalf("focused task=%+v err=%v", task, err)
	}
	now := time.Now().UTC()
	firstOrder := order("synthetic", 100)
	firstOrder.IdentityVerified = false
	secondOrder := firstOrder
	secondOrder.RemainingQuantity = 50
	first := scan("one", task.ID, "first", 1, now, firstOrder)
	second := scan("one", task.ID, "second", 1, now.Add(time.Second), secondOrder)
	first.LeaseToken, second.LeaseToken = task.LeaseToken, task.LeaseToken
	second.ContentHash = strings.Repeat("e", 64)
	if _, err := system.SaveScan(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := system.SaveScan(ctx, second); err != nil {
		t.Fatal(err)
	}
	evidence, err := system.store.Evidence(ctx)
	if err != nil || len(evidence) != 1 || evidence[0].FillEvents != 1 || evidence[0].FilledUnits24h != 50 {
		t.Fatalf("bounded synthetic reduction was not retained: evidence=%+v err=%v", evidence, err)
	}
	var level int
	if err := system.store.db.QueryRow(`SELECT confirmation_level FROM fill_events`).Scan(&level); err != nil || level != 1 {
		t.Fatalf("synthetic confirmation level=%d err=%v", level, err)
	}
}

func TestCandidateBuilderIsQuantityAndEvidenceSafe(t *testing.T) {
	now := time.Now().UTC()
	evidence := Evidence{Signature: "minecraft:diamond_block", ItemID: "minecraft:diamond_block", DisplayName: "Diamond Block",
		Tier: "actionable", CompleteScans: 10, FillEvents: 8, DistinctOrders: 3, FilledUnits24h: 640, AvailableUnits: 640,
		BestUnitRewardCents: 400_000, BestPricePosition: 1, ObservedQuantity: 64, MaxStackSize: 64, LastSeenAt: now, Stable: true, SignatureComplete: true}
	valuation := market.Valuation{Signature: evidence.Signature, QuickSellValue: 5_000, QuantityQuickSell: 5_000,
		PricingQuantity: 64, Volume24h: 640, PriceSellerCount: 4, ConfidenceBPS: 9_000,
		ExpectedSellMinutes: 30, ActiveBestAsk: 3_000, ActiveDepth: 20, GeneratedAt: now}
	values := buildCandidates([]Evidence{evidence}, map[string]market.Valuation{evidence.Signature: valuation}, Config{AuctionFeeBPS: 250, CandidateLimit: 100}, now)
	if len(values) != 2 {
		t.Fatalf("candidates=%+v", values)
	}
	for _, candidate := range values {
		if candidate.State != "READY" || candidate.PriorityRank <= 0 || candidate.PriorityScore <= 0 || candidate.Quantity != 64 || candidate.InventorySlots != 1 || candidate.ConservativeProfit <= 0 {
			t.Fatalf("unsafe candidate: %+v", candidate)
		}
		if candidate.Route == "ORDER_TO_AUCTION" && candidate.AcquisitionCost != 256_001 {
			t.Fatalf("competitive order did not add exactly one cent per unit: %+v", candidate)
		}
		if candidate.Route == "ORDER_TO_AUCTION" && (candidate.OrderUnitRewardCents != 400_001 || candidate.TargetListPrice != 320_000) {
			t.Fatalf("prepared transaction values are incorrect: %+v", candidate)
		}
		if candidate.Route == "ORDER_TO_AUCTION" && candidate.MaxOrderQuantity != 640 {
			t.Fatalf("market-sized order quantity was capped by auction slots: %+v", candidate)
		}
	}
	valuation.QuantityQuickSell = 0
	if got := buildCandidates([]Evidence{evidence}, map[string]market.Valuation{evidence.Signature: valuation}, Config{}, now); len(got) != 0 {
		t.Fatalf("stack escaped exact-quantity gate: %+v", got)
	}
	valuation.QuantityQuickSell = 5_000
	valuation.PricingQuantity = 1
	if got := buildCandidates([]Evidence{evidence}, map[string]market.Valuation{evidence.Signature: valuation}, Config{}, now); len(got) != 2 || got[0].Quantity != 1 || got[0].MaxStackSize != 64 {
		t.Fatalf("exact singular exit was not preserved below the stack cap: %+v", got)
	}
	valuation.PricingQuantity = 64
	evidence.AvailableUnits = 0
	evidence.FilledUnits24h = 0
	valuation.QuantityQuickSell = 5_000
	for _, value := range buildCandidates([]Evidence{evidence}, map[string]market.Valuation{evidence.Signature: valuation}, Config{}, now) {
		if value.Route == "ORDER_TO_AUCTION" && (value.State != "READY" || value.ExecutableBatches != 1 || value.MaxOrderQuantity != 64) {
			t.Fatalf("profitable filler did not stay bounded to one exploratory stack: %+v", value)
		}
		if value.Route == "ORDER_TO_AUCTION" && value.ResearchBatches > 1 {
			t.Fatalf("competing order demand became speculative fill capacity: %+v", value)
		}
	}
	evidence.AvailableUnits = 640
	evidence.FilledUnits24h = 640
	evidence.SignatureComplete = false
	for _, value := range buildCandidates([]Evidence{evidence}, map[string]market.Valuation{evidence.Signature: valuation}, Config{}, now) {
		if value.PriorityRank != 0 || value.PriorityScore != 0 {
			t.Fatalf("modifier-ambiguous evidence received priority: %+v", value)
		}
	}
}

func TestCandidateOrderObservationTrustWindow(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	evidence := Evidence{Signature: "minecraft:diamond_block", ItemID: "minecraft:diamond_block", DisplayName: "Diamond Block",
		Tier: "actionable", CompleteScans: 10, FillEvents: 8, DistinctOrders: 3, FilledUnits24h: 640, AvailableUnits: 640,
		BestUnitRewardCents: 400_000, BestPricePosition: 1, ObservedQuantity: 64, MaxStackSize: 64, Stable: true, SignatureComplete: true}
	valuation := market.Valuation{Signature: evidence.Signature, QuickSellValue: 5_000, QuantityQuickSell: 5_000,
		PricingQuantity: 64, Volume24h: 640, PriceSellerCount: 4, ConfidenceBPS: 9_000,
		ExpectedSellMinutes: 30, ActiveBestAsk: 3_000, ActiveDepth: 20, GeneratedAt: now}

	evidence.LastSeenAt = now.Add(-59 * time.Minute)
	values := buildCandidates([]Evidence{evidence}, map[string]market.Valuation{evidence.Signature: valuation}, Config{}, now)
	if len(values) != 2 {
		t.Fatalf("trusted observation produced no routes: %+v", values)
	}
	for _, value := range values {
		if value.State != "READY" {
			t.Fatalf("59-minute order observation was discarded: %+v", value)
		}
	}
	evidence.LastSeenAt = now.Add(-61 * time.Minute)
	values = buildCandidates([]Evidence{evidence}, map[string]market.Valuation{evidence.Signature: valuation}, Config{}, now)
	if len(values) != 2 {
		t.Fatalf("expired observation disappeared instead of becoming stale: %+v", values)
	}
	for _, value := range values {
		if value.State != "STALE" {
			t.Fatalf("expired order observation remained actionable: %+v", value)
		}
	}
}

func TestCandidateUsesCalibratedExactExitConfidenceAndVolumeFreshness(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	evidence := Evidence{Signature: "minecraft:diamond_block", ItemID: "minecraft:diamond_block", DisplayName: "Diamond Block",
		Tier: "actionable", CompleteScans: 10, FillEvents: 8, DistinctOrders: 3, FilledUnits24h: 640, AvailableUnits: 640,
		BestUnitRewardCents: 400_000, ObservedQuantity: 64, MaxStackSize: 64, LastSeenAt: now, Stable: true, SignatureComplete: true}
	valuation := market.Valuation{Signature: evidence.Signature, QuickSellValue: 5_000, QuantityQuickSell: 5_000,
		PricingQuantity: 64, Volume24h: 5, PriceSellerCount: 3, ConfidenceBPS: minimumExactExitConfidenceBPS,
		ExpectedSellMinutes: 30, ActiveBestAsk: 3_000, ActiveDepth: 20, GeneratedAt: now,
		PriceReferenceAgeSeconds: int64((9 * time.Hour).Seconds())}
	values := buildCandidates([]Evidence{evidence}, map[string]market.Valuation{evidence.Signature: valuation}, Config{}, now)
	if len(values) != 2 || values[0].State != "READY" || values[1].State != "READY" {
		t.Fatalf("volume-adjusted five-sale market should remain ready for 9h: %+v", values)
	}
	valuation.ConfidenceBPS--
	values = buildCandidates([]Evidence{evidence}, map[string]market.Valuation{evidence.Signature: valuation}, Config{}, now)
	if len(values) != 2 || values[0].State != "READY" || values[0].ExecutableBatches != 1 {
		t.Fatalf("moderate-confidence exact exit was not admitted as a one-stack filler: %+v", values)
	}
	valuation.ConfidenceBPS = minimumFillerExitConfidenceBPS - 1
	values = buildCandidates([]Evidence{evidence}, map[string]market.Valuation{evidence.Signature: valuation}, Config{}, now)
	if len(values) != 2 || values[0].State == "READY" {
		t.Fatalf("below-filler confidence became ready: %+v", values)
	}
	valuation.ConfidenceBPS = minimumExactExitConfidenceBPS
	valuation.PriceReferenceAgeSeconds = int64((10 * time.Hour).Seconds())
	values = buildCandidates([]Evidence{evidence}, map[string]market.Valuation{evidence.Signature: valuation}, Config{}, now)
	if len(values) != 2 || values[0].State != "HOLD" {
		t.Fatalf("stale five-sale market exceeded its adaptive freshness limit: %+v", values)
	}
}

func TestCandidateAdmitsThinProfitableMarketAsOneStackFiller(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	evidence := Evidence{Signature: "minecraft:bone_block", ItemID: "minecraft:bone_block", DisplayName: "Bone Blocks",
		Tier: "research", CompleteScans: 6, BestUnitRewardCents: 10_000, ObservedQuantity: 64, MaxStackSize: 64,
		LastSeenAt: now, Stable: true, SignatureComplete: true}
	valuation := market.Valuation{Signature: evidence.Signature, QuickSellValue: 150, QuantityQuickSell: 150,
		PricingQuantity: 64, Volume24h: 2, PriceSellerCount: 2, ConfidenceBPS: minimumFillerExitConfidenceBPS,
		ExpectedSellMinutes: 720, GeneratedAt: now, RiskFlags: []string{"low_price_liquidity", "seller_concentration"}}
	values := buildCandidates([]Evidence{evidence}, map[string]market.Valuation{evidence.Signature: valuation}, Config{AuctionFeeBPS: 250}, now)
	if len(values) != 1 {
		t.Fatalf("filler candidates=%+v", values)
	}
	value := values[0]
	if value.Route != "ORDER_TO_AUCTION" || value.State != "READY" || value.OrderTier != "research" ||
		value.ExecutableBatches != 1 || value.MaxOrderQuantity != 64 || value.ConservativeProfit <= 0 || value.PriorityRank <= 0 {
		t.Fatalf("unsafe filler candidate: %+v", value)
	}
	valuation.PriceSellerCount = 1
	values = buildCandidates([]Evidence{evidence}, map[string]market.Valuation{evidence.Signature: valuation}, Config{AuctionFeeBPS: 250}, now)
	if len(values) != 1 || values[0].State == "READY" {
		t.Fatalf("single-seller exit became filler-ready: %+v", values)
	}
}

func TestActionableEvidenceWithoutAuctionQualificationRemainsResearch(t *testing.T) {
	now := time.Now().UTC()
	evidence := Evidence{Signature: "minecraft:sponge", ItemID: "minecraft:sponge", DisplayName: "Sponge",
		Tier: "actionable", CompleteScans: 10, FillEvents: 5, DistinctOrders: 3, FilledUnits24h: 64,
		BestUnitRewardCents: 10_000, ObservedQuantity: 1, MaxStackSize: 64, LastSeenAt: now,
		Stable: true, SignatureComplete: true}
	valuation := market.Valuation{Signature: evidence.Signature, PricingQuantity: 1, QuickSellValue: 200,
		Volume24h: 2, PriceSellerCount: 1, ConfidenceBPS: 9_000, ExpectedSellMinutes: 30, GeneratedAt: now}

	values := buildCandidates([]Evidence{evidence}, map[string]market.Valuation{evidence.Signature: valuation}, Config{}, now)
	if len(values) != 1 || values[0].State != "RESEARCH" {
		t.Fatalf("non-qualified actionable evidence leaked into candidate state: %+v", values)
	}
}

func TestCandidateUsesCurrentFocusedRewardForExecutionEconomics(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	evidence := Evidence{Signature: "minecraft:diamond_block", ItemID: "minecraft:diamond_block", DisplayName: "Diamond Block",
		Tier: "actionable", CompleteScans: 10, FillEvents: 8, DistinctOrders: 3, FilledUnits24h: 640, AvailableUnits: 640,
		BestUnitRewardCents: 400_000, FocusedUnitRewardCents: 350_000, FocusedSeenAt: now, BestPricePosition: 1,
		ObservedQuantity: 64, MaxStackSize: 64, LastSeenAt: now, Stable: true, SignatureComplete: true}
	valuation := market.Valuation{Signature: evidence.Signature, QuickSellValue: 5_000, QuantityQuickSell: 5_000,
		PricingQuantity: 64, Volume24h: 640, PriceSellerCount: 4, ConfidenceBPS: 9_000,
		ExpectedSellMinutes: 30, ActiveBestAsk: 3_000, ActiveDepth: 20, GeneratedAt: now}
	values := buildCandidates([]Evidence{evidence}, map[string]market.Valuation{evidence.Signature: valuation}, Config{}, now)
	if len(values) != 2 {
		t.Fatalf("focused economics produced no routes: %+v", values)
	}
	for _, value := range values {
		if value.OrderUnitRewardCents != 350_000+map[bool]int64{true: 1, false: 0}[value.Route == "ORDER_TO_AUCTION"] {
			t.Fatalf("route retained stale discovery reward: %+v", value)
		}
	}
}

func TestCandidateCrossesAbbreviatedPriceBucketAndRepricesEconomics(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	evidence := Evidence{Signature: "minecraft:netherite_scrap", ItemID: "minecraft:netherite_scrap", DisplayName: "Netherite Scrap",
		Tier: "actionable", CompleteScans: 10, FillEvents: 8, DistinctOrders: 3, FilledUnits24h: 20, AvailableUnits: 20,
		BestUnitRewardCents: 130_000_000, BestCompetitiveUnitRewardCents: 140_000_000,
		ObservedQuantity: 1, MaxStackSize: 64, LastSeenAt: now, Stable: true, SignatureComplete: true}
	valuation := market.Valuation{Signature: evidence.Signature, QuickSellValue: 1_500_000, PricingQuantity: 1,
		Volume24h: 20, PriceSellerCount: 4, ConfidenceBPS: 9_000, ExpectedSellMinutes: 30, GeneratedAt: now}

	values := buildCandidates([]Evidence{evidence}, map[string]market.Valuation{evidence.Signature: valuation}, Config{}, now)
	var orderCandidate Candidate
	for _, value := range values {
		if value.Route == "ORDER_TO_AUCTION" {
			orderCandidate = value
		}
	}
	if orderCandidate.OrderUnitRewardCents != 140_000_000 || orderCandidate.ObservedOrderUnitRewardCents != 130_000_000 ||
		orderCandidate.AcquisitionCost != 1_400_000 {
		t.Fatalf("abbreviated price bucket was not crossed: %+v", orderCandidate)
	}

	valuation.QuickSellValue = 1_350_000
	values = buildCandidates([]Evidence{evidence}, map[string]market.Valuation{evidence.Signature: valuation}, Config{}, now)
	for _, value := range values {
		if value.Route == "ORDER_TO_AUCTION" && value.State != "REJECTED" {
			t.Fatalf("bucket-top acquisition cost was not used by profitability gate: %+v", value)
		}
	}
}

func TestStablePricesRejectsBroadMovementButIgnoresOneTransientSpike(t *testing.T) {
	steadyWithSpike := []int64{100, 100, 101, 99, 100, 102, 100, 101, 99, 250, 100, 101}
	if !stablePrices(steadyWithSpike) {
		t.Fatal("one transient top-order spike erased a stable long-lived market")
	}
	if stablePrices([]int64{100, 100, 100, 100, 120, 121, 122, 123, 124, 125, 126, 127}) {
		t.Fatal("sustained order-price movement was classified as stable")
	}
}

func TestAuctionResearchTargetsPreferValuableLiquidCanonicalItems(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	values := map[string]market.Valuation{
		"diamond":   {Signature: "minecraft:diamond", BaseSignature: "minecraft:diamond", QuickSellValue: 5_000, Volume24h: 20, PriceSellerCount: 3, ConfidenceBPS: 8_000, GeneratedAt: now},
		"iron":      {Signature: "minecraft:iron_ingot", BaseSignature: "minecraft:iron_ingot", QuickSellValue: 1_000, Volume24h: 20, PriceSellerCount: 3, ConfidenceBPS: 8_000, GeneratedAt: now},
		"netherite": {Signature: "minecraft:netherite_ingot", BaseSignature: "minecraft:netherite_ingot", QuickSellValue: 6_000_000, Volume24h: 2, PriceSellerCount: 2, ConfidenceBPS: 3_000, GeneratedAt: now},
		"unsafe":    {Signature: "minecraft:diamond|enchanted", BaseSignature: "minecraft:diamond|enchanted", QuickSellValue: 100_000, Volume24h: 20, PriceSellerCount: 3, ConfidenceBPS: 8_000, GeneratedAt: now},
		"thin":      {Signature: "minecraft:netherite_ingot", BaseSignature: "minecraft:netherite_ingot", QuickSellValue: 50_000, Volume24h: 1, PriceSellerCount: 1, ConfidenceBPS: 8_000, GeneratedAt: now},
	}
	targets := auctionResearchTargets(values, now, 10)
	if len(targets) != 3 || targets[0] != "minecraft:netherite_ingot" || targets[1] != "minecraft:diamond" || targets[2] != "minecraft:iron_ingot" {
		t.Fatalf("auction shortlist=%v", targets)
	}
	if !canonicalBaseItem("minecraft:redstone_block") || canonicalBaseItem("minecraft:redstone block") || canonicalBaseItem("minecraft:my") ||
		canonicalBaseItem("minecraft:netherite_spear") || canonicalBaseItem("minecraft:shulker_box") || !canonicalBaseItem("minecraft:dragon_head") {
		t.Fatal("canonical search safety policy is inconsistent")
	}
}

func TestAuctionShortlistCanScheduleItemBeforeOrderEvidence(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	if _, err = system.Register(ctx, ObserverRegistration{ObserverID: "observer", ParserVersion: "p1", ProxyLabel: "proxy"}); err != nil {
		t.Fatal(err)
	}
	targets := []string{"minecraft:diamond_block"}
	system.research.Store(&targets)
	if err = system.queueAutomaticResearch(ctx); err != nil {
		t.Fatal(err)
	}
	task, err := system.LeaseTask(ctx, "observer")
	if err != nil || task == nil || task.Kind != "focused_watch" || task.Signature != "minecraft:diamond_block" {
		t.Fatalf("auction-prior task=%+v err=%v", task, err)
	}
}

func TestAuctionShortlistPrecedesReplaceableFillerRefresh(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	if _, err = system.Register(ctx, ObserverRegistration{ObserverID: "observer", ParserVersion: "p1", ProxyLabel: "proxy"}); err != nil {
		t.Fatal(err)
	}
	system.candidates.Store(&CandidateFeed{Candidates: []Candidate{{
		Route: "ORDER_TO_AUCTION", State: "READY", OrderTier: "research", Signature: "minecraft:blue_ice",
		SignatureComplete: true, PriorityScore: 100, TargetListPrice: 10_000, ConservativeProfit: 1_000,
	}}})
	targets := []string{"minecraft:netherite_ingot"}
	system.research.Store(&targets)
	if err = system.queueAutomaticResearch(ctx); err != nil {
		t.Fatal(err)
	}
	task, err := system.LeaseTask(ctx, "observer")
	if err != nil || task == nil || task.Signature != "minecraft:netherite_ingot" {
		t.Fatalf("price-first task=%+v err=%v", task, err)
	}
}

func TestExplorationCooldownIsLongerThanCoreRecheck(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	system.store.now = func() time.Time { return now }
	signature := "minecraft:netherite_ingot"
	if err = system.store.QueueAutomaticResearch(ctx, []string{signature}, map[string]int{signature: 50}, 0, 5*time.Minute, 20*time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err = system.store.db.Exec(`UPDATE tasks SET state='completed'`); err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Minute)
	if err = system.store.QueueAutomaticResearch(ctx, []string{signature}, map[string]int{signature: 50}, 0, 5*time.Minute, 20*time.Minute); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = system.store.db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("exploration cooldown count=%d err=%v", count, err)
	}
	if err = system.store.QueueAutomaticResearch(ctx, []string{signature}, map[string]int{signature: 75}, 0, 5*time.Minute, 20*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err = system.store.db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("core cooldown count=%d err=%v", count, err)
	}
}

func TestParserRolloutClearsCompletedAutomaticCooldowns(t *testing.T) {
	system, err := NewSystem(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Close() })
	ctx := context.Background()
	registration := ObserverRegistration{ObserverID: "observer", ParserVersion: "p1", ProxyLabel: "proxy"}
	if _, err = system.Register(ctx, registration); err != nil {
		t.Fatal(err)
	}
	if _, err = system.store.db.Exec(`INSERT INTO tasks(id,kind,signature,priority,desired_freshness_ms,parser_schema,state,automatic,created_ms,updated_ms)
		VALUES('old-auto','focused_watch','minecraft:blue_ice',50,1000,?,'completed',1,100,100)`, SchemaVersion); err != nil {
		t.Fatal(err)
	}
	registration.ParserVersion = "p2"
	if _, err = system.Register(ctx, registration); err != nil {
		t.Fatal(err)
	}
	var updated int64
	if err = system.store.db.QueryRow(`SELECT updated_ms FROM tasks WHERE id='old-auto'`).Scan(&updated); err != nil || updated != 0 {
		t.Fatalf("automatic cooldown timestamp=%d err=%v", updated, err)
	}
}

func TestBuildCandidatesUsesAPIProvenExitQuantityBelowStackLimit(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	evidence := Evidence{ItemID: "minecraft:netherite_ingot", Signature: "minecraft:netherite_ingot", DisplayName: "Netherite Ingot",
		Tier: "research", CompleteScans: 4, BestUnitRewardCents: 5_000_000, ObservedQuantity: 1, MaxStackSize: 64,
		LastSeenAt: now, Stable: true, SignatureComplete: true}
	valuation := market.Valuation{Signature: evidence.Signature, BaseSignature: evidence.Signature, PricingQuantity: 1,
		QuickSellValue: 60_000, SingularQuickSell: 60_000, Volume24h: 8, PriceSellerCount: 3,
		ConfidenceBPS: 8_000, ExpectedSellMinutes: 30, GeneratedAt: now}
	values := buildCandidates([]Evidence{evidence}, map[string]market.Valuation{evidence.Signature: valuation}, Config{}, now)
	for _, value := range values {
		if value.Route == "ORDER_TO_AUCTION" {
			if value.Quantity != 1 || value.MaxStackSize != 64 {
				t.Fatalf("exit quantity=%d stack cap=%d", value.Quantity, value.MaxStackSize)
			}
			return
		}
	}
	t.Fatal("missing order-to-auction candidate")
}

func TestCandidateDisplaysClearingPriceWhileScoringQuickSale(t *testing.T) {
	now := time.Date(2026, 8, 27, 22, 0, 0, 0, time.UTC)
	evidence := Evidence{ItemID: "minecraft:netherite_scrap", Signature: "minecraft:netherite_scrap", DisplayName: "Netherite Scrap",
		Tier: "research", CompleteScans: 4, BestUnitRewardCents: 160_000_000, ObservedQuantity: 1, MaxStackSize: 64,
		LastSeenAt: now, Stable: true, SignatureComplete: true}
	valuation := market.Valuation{Signature: evidence.Signature, PricingQuantity: 1, FairValue: 1_800_000,
		QuickSellValue: 1_746_000, SingularQuickSell: 1_746_000, ActiveReferenceAsk: 2_000_000,
		Volume24h: 25, PriceSellerCount: 16, ConfidenceBPS: 8_000, ExpectedSellMinutes: 30, GeneratedAt: now}
	values := buildCandidates([]Evidence{evidence}, map[string]market.Valuation{evidence.Signature: valuation}, Config{AuctionFeeBPS: 250}, now)
	for _, value := range values {
		if value.Route != "ORDER_TO_AUCTION" {
			continue
		}
		if value.TargetListPrice != 1_800_000 {
			t.Fatalf("display target=%d want observed clearing price 1800000", value.TargetListPrice)
		}
		if value.ExpectedProceeds != 1_702_350 {
			t.Fatalf("risk proceeds=%d want quick-sale value after fees 1702350", value.ExpectedProceeds)
		}
		return
	}
	t.Fatal("missing order-to-auction candidate")
}

func TestCandidateScoringSaturatesInsteadOfOverflowing(t *testing.T) {
	value := candidate(Candidate{
		AcquisitionCost:      1,
		ExpectedProceeds:     math.MaxInt64,
		ConfidenceBPS:        10_000,
		CompletionBPS:        10_000,
		ExpectedCycleMinutes: 1,
		InventorySlots:       1,
	})
	if value.ConservativeProfit != math.MaxInt64-1 {
		t.Fatalf("conservative profit wrapped: %d", value.ConservativeProfit)
	}
	if value.RiskAdjustedProfitDay != math.MaxInt64 {
		t.Fatalf("daily score did not saturate: %d", value.RiskAdjustedProfitDay)
	}
	if value.MarginBPS != math.MaxInt32 {
		t.Fatalf("margin did not clamp: %d", value.MarginBPS)
	}
}

func TestCandidateScoringAppliesConfidenceAndCompletionOnce(t *testing.T) {
	value := candidate(Candidate{AcquisitionCost: 1_000, ExpectedProceeds: 2_000, ConfidenceBPS: 8_000,
		CompletionBPS: 5_000, ExpectedCycleMinutes: 1440, InventorySlots: 1, ResearchBatches: 1, SignatureComplete: true})
	if value.ConservativeProfit != 800 {
		t.Fatalf("confidence-adjusted profit=%d want=800", value.ConservativeProfit)
	}
	if value.RiskAdjustedProfitDay != 400 || value.PriorityScore != 400 {
		t.Fatalf("completion was not applied exactly once: daily=%d priority=%d", value.RiskAdjustedProfitDay, value.PriorityScore)
	}
}

func scan(observer, task, session string, page int, at time.Time, values ...OrderObservation) ScanBatch {
	return ScanBatch{SchemaVersion: SchemaVersion, ObserverID: observer, TaskID: task, SessionID: session,
		ContentHash: fmt.Sprintf("%064x", page+1), ScreenTitle: "Orders", Page: page, Complete: true, ObservedAt: at, Orders: append([]OrderObservation(nil), values...)}
}

func order(key string, remaining int64) OrderObservation {
	return OrderObservation{OrderKey: key, ItemID: "minecraft:diamond", Signature: "minecraft:diamond", DisplayName: "Diamond",
		Quantity: 1, MaxStackSize: 64, UnitRewardCents: 100, CompetitiveUnitRewardCents: 101, RequestedQuantity: 100, RemainingQuantity: remaining, IdentityVerified: true,
		Owner: "buyer", PricePosition: 1, Slot: 1, RawFieldHash: strings.Repeat("a", 64), SignatureComplete: true}
}

func BenchmarkBuildCandidateFrontier(b *testing.B) {
	now := time.Now().UTC()
	evidence := make([]Evidence, 100)
	valuations := make(map[string]market.Valuation, len(evidence))
	for index := range evidence {
		signature := fmt.Sprintf("minecraft:item_%d", index)
		evidence[index] = Evidence{Signature: signature, ItemID: signature, DisplayName: signature, Tier: "actionable",
			CompleteScans: 10, FillEvents: 8, DistinctOrders: 3, FilledUnits24h: 640, AvailableUnits: 640, BestUnitRewardCents: 400_000,
			ObservedQuantity: 64, MaxStackSize: 64, LastSeenAt: now, Stable: true, SignatureComplete: true, BestPricePosition: 1}
		valuations[signature] = market.Valuation{Signature: signature, QuickSellValue: 5_000, QuantityQuickSell: 5_000,
			PricingQuantity: 64, Volume24h: 640, PriceSellerCount: 4, ConfidenceBPS: 9_000,
			ExpectedSellMinutes: 30, ActiveBestAsk: 3_000, ActiveDepth: 20, GeneratedAt: now}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		_ = buildCandidates(evidence, valuations, Config{AuctionFeeBPS: 250, CandidateLimit: 100}, now)
	}
}

func BenchmarkEvidencePopulated(b *testing.B) {
	path := strings.TrimSpace(os.Getenv("DN_BENCHMARK_DB"))
	if path == "" {
		b.Skip("set DN_BENCHMARK_DB to a disposable populated SQLite copy")
	}
	store, err := OpenStore(path)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		if _, err := store.Evidence(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
