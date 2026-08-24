package orders

import (
	"context"
	"fmt"
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
	historical.UnitReward = 10_000
	historical.PricePosition = 1
	if _, err := system.SaveScan(ctx, scan("one", "", "old", 1, now.Add(-time.Hour), historical)); err != nil {
		t.Fatal(err)
	}
	for index, age := range []time.Duration{90 * time.Second, 45 * time.Second, 0} {
		current := order("same", 100)
		current.UnitReward = 100
		current.PricePosition = 3
		if _, err := system.SaveScan(ctx, scan("one", "", fmt.Sprintf("current-%d", index), index+2, now.Add(-age), current)); err != nil {
			t.Fatal(err)
		}
	}
	evidence, err := system.store.Evidence(ctx)
	if err != nil || len(evidence) != 1 {
		t.Fatalf("evidence=%+v err=%v", evidence, err)
	}
	if evidence[0].BestUnitReward != 100 || evidence[0].BestPricePosition != 3 {
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
	base := time.Now().UTC().Add(-20 * time.Minute)
	observedAt := []time.Duration{0, 10 * time.Minute, 18 * time.Minute, 18*time.Minute + 45*time.Second, 19*time.Minute + 15*time.Second, 20 * time.Minute}
	orders := []OrderObservation{order("a", 100), order("b", 100), order("c", 100)}
	for index := 0; index < 6; index++ {
		for orderIndex := range orders {
			if index > 0 && orderIndex <= index%3 {
				orders[orderIndex].RemainingQuantity -= 1
			}
		}
		batch := scan("one", "", fmt.Sprintf("session-%d", index), index, base.Add(observedAt[index]), orders...)
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
	if evidence[0].FillEvents < 5 || evidence[0].DistinctOrders < 3 || evidence[0].Tier != "actionable" {
		t.Fatalf("expected actionable fill evidence, got %+v", evidence[0])
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

func TestCandidateBuilderIsQuantityAndEvidenceSafe(t *testing.T) {
	now := time.Now().UTC()
	evidence := Evidence{Signature: "minecraft:diamond_block", ItemID: "minecraft:diamond_block", DisplayName: "Diamond Block",
		Tier: "actionable", CompleteScans: 10, FillEvents: 8, DistinctOrders: 3, FilledUnits24h: 640, AvailableUnits: 640,
		BestUnitReward: 4_000, BestPricePosition: 1, ObservedQuantity: 64, MaxStackSize: 64, LastSeenAt: now, Stable: true}
	valuation := market.Valuation{Signature: evidence.Signature, QuickSellValue: 5_000, QuantityQuickSell: 5_000,
		PricingQuantity: 64, Volume24h: 640, PriceSellerCount: 4, ConfidenceBPS: 9_000,
		ExpectedSellMinutes: 30, ActiveBestAsk: 3_000, ActiveDepth: 20, GeneratedAt: now}
	values := buildCandidates([]Evidence{evidence}, map[string]market.Valuation{evidence.Signature: valuation}, Config{AuctionFeeBPS: 250, CandidateLimit: 100}, now)
	if len(values) != 2 {
		t.Fatalf("candidates=%+v", values)
	}
	for _, candidate := range values {
		if candidate.State != "READY" || candidate.Quantity != 64 || candidate.InventorySlots != 1 || candidate.ConservativeProfit <= 0 {
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
	}
}

func scan(observer, task, session string, page int, at time.Time, values ...OrderObservation) ScanBatch {
	return ScanBatch{SchemaVersion: SchemaVersion, ObserverID: observer, TaskID: task, SessionID: session,
		ContentHash: fmt.Sprintf("%064x", page+1), ScreenTitle: "Orders", Page: page, Complete: true, ObservedAt: at, Orders: append([]OrderObservation(nil), values...)}
}

func order(key string, remaining int64) OrderObservation {
	return OrderObservation{OrderKey: key, ItemID: "minecraft:diamond", Signature: "minecraft:diamond", DisplayName: "Diamond",
		Quantity: 1, MaxStackSize: 64, UnitReward: 100, RequestedQuantity: 100, RemainingQuantity: remaining,
		Owner: "buyer", PricePosition: 1, Slot: 1, RawFieldHash: strings.Repeat("a", 64), SignatureComplete: true}
}

func BenchmarkBuildCandidateFrontier(b *testing.B) {
	now := time.Now().UTC()
	evidence := make([]Evidence, 100)
	valuations := make(map[string]market.Valuation, len(evidence))
	for index := range evidence {
		signature := fmt.Sprintf("minecraft:item_%d", index)
		evidence[index] = Evidence{Signature: signature, ItemID: signature, DisplayName: signature, Tier: "actionable",
			CompleteScans: 10, FillEvents: 8, DistinctOrders: 3, FilledUnits24h: 640, AvailableUnits: 640, BestUnitReward: 4_000,
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
