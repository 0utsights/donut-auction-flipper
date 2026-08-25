package orders

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
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
	_, _ = system.Register(ctx, ObserverRegistration{ObserverID: "one", ParserVersion: "p1", ProxyLabel: "proxy"})
	batch := scan("one", "", "capture", 0, time.Now().UTC(), order("unverified", 100))
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
	historical.PricePosition = 1
	if _, err := system.SaveScan(ctx, scan("one", "", "old", 1, now.Add(-time.Hour), historical)); err != nil {
		t.Fatal(err)
	}
	for index, age := range []time.Duration{90 * time.Second, 45 * time.Second, 0} {
		current := order("same", 100)
		current.UnitRewardCents = 100
		current.PricePosition = 3
		if _, err := system.SaveScan(ctx, scan("one", "", fmt.Sprintf("current-%d", index), index+2, now.Add(-age), current)); err != nil {
			t.Fatal(err)
		}
	}
	evidence, err := system.store.Evidence(ctx)
	if err != nil || len(evidence) != 1 {
		t.Fatalf("evidence=%+v err=%v", evidence, err)
	}
	if evidence[0].BestUnitRewardCents != 100 || evidence[0].BestPricePosition != 1 {
		t.Fatalf("historical price leaked into current evidence: %+v", evidence[0])
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
		batch := scan("one", "", "steady-session", 1, base.Add(observedAt[index]), orders...)
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
	if evidence[0].FillEvents != 6 || evidence[0].DistinctOrders != 3 || evidence[0].Tier != "actionable" {
		t.Fatalf("expected actionable fill evidence, got %+v", evidence[0])
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
	}
	valuation.QuantityQuickSell = 0
	if got := buildCandidates([]Evidence{evidence}, map[string]market.Valuation{evidence.Signature: valuation}, Config{}, now); len(got) != 0 {
		t.Fatalf("stack escaped exact-quantity gate: %+v", got)
	}
	evidence.AvailableUnits = 0
	evidence.FilledUnits24h = 0
	valuation.QuantityQuickSell = 5_000
	for _, value := range buildCandidates([]Evidence{evidence}, map[string]market.Valuation{evidence.Signature: valuation}, Config{}, now) {
		if value.State == "READY" || value.ExecutableBatches != 0 {
			t.Fatalf("candidate invented executable volume: %+v", value)
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
		Quantity: 1, MaxStackSize: 64, UnitRewardCents: 100, RequestedQuantity: 100, RemainingQuantity: remaining,
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
