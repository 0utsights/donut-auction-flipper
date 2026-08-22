package market

import (
	"fmt"
	"testing"
	"time"
)

func TestEngineMaintainsActiveDepthAndBestAsk(t *testing.T) {
	e := NewEngine()
	now := time.Now().UTC()
	ts := make([]Transaction, 10)
	for i := range ts {
		ts[i] = Transaction{SellerName: fmt.Sprint(i), Item: Item{ID: "elytra", Quantity: 1}, TotalPrice: 300_000_000, SoldAt: now.Add(-time.Duration(i) * time.Minute)}
	}
	e.AddTransactions(ts)
	a, _ := e.Observe(Listing{SellerName: "a", Item: Item{ID: "elytra", Quantity: 1}, TotalPrice: 290_000_000})
	b, _ := e.Observe(Listing{SellerName: "b", Item: Item{ID: "elytra", Quantity: 1}, TotalPrice: 280_000_000})
	v, ok := e.Valuation("minecraft:elytra")
	if !ok || v.ActiveDepth != 2 || v.ActiveBestAsk != 280_000_000 {
		t.Fatalf("bad active aggregate: %+v", v)
	}
	e.RemoveListing(b.Fingerprint)
	v, _ = e.Valuation("minecraft:elytra")
	if v.ActiveDepth != 1 || v.ActiveBestAsk != 290_000_000 {
		t.Fatalf("bad aggregate after remove: %+v (a=%s)", v, a.Fingerprint)
	}
}

func TestEngineExpiresStaleActiveListings(t *testing.T) {
	e := NewEngine()
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }
	ts := make([]Transaction, 10)
	for i := range ts {
		ts[i] = Transaction{SellerName: fmt.Sprint(i), Item: Item{ID: "elytra", Quantity: 1}, TotalPrice: 300_000_000, SoldAt: now.Add(-time.Duration(i) * time.Minute)}
	}
	e.AddTransactions(ts)
	e.Observe(Listing{SellerName: "stale-low-ask", Item: Item{ID: "elytra", Quantity: 1}, TotalPrice: 100_000_000, ExpiresAt: now.Add(time.Second)})
	v, _ := e.Valuation("minecraft:elytra")
	if v.ActiveBestAsk != 100_000_000 {
		t.Fatalf("listing was not active: %+v", v)
	}
	if expired := e.SweepExpired(now.Add(2 * time.Second)); expired != 1 {
		t.Fatalf("expired=%d want=1", expired)
	}
	v, _ = e.Valuation("minecraft:elytra")
	if v.ActiveDepth != 0 || v.ActiveBestAsk != 0 || v.QuickSellValue <= 100_000_000 {
		t.Fatalf("stale ask still affects valuation: %+v", v)
	}
}

func TestEngineFallbackTTLExpiresListingsWithoutDeadline(t *testing.T) {
	e := NewEngine()
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }
	e.Observe(Listing{SellerName: "unknown-expiry", Item: Item{ID: "spawner", Quantity: 1}, TotalPrice: 20_000_000})
	if expired := e.SweepExpired(now.Add(activeListingFallbackTTL - time.Second)); expired != 0 {
		t.Fatalf("listing expired early: %d", expired)
	}
	if expired := e.SweepExpired(now.Add(activeListingFallbackTTL)); expired != 1 {
		t.Fatalf("fallback TTL did not expire listing: %d", expired)
	}
}

func TestEngineBuildsConfidencePenalizedBaseFallback(t *testing.T) {
	e := NewEngine()
	now := time.Now().UTC()
	for i := 0; i < 10; i++ {
		e.AddTransactions([]Transaction{{SellerName: fmt.Sprint(i), Item: Item{ID: "netherite_sword", Quantity: 1, Enchantments: map[string]int{"sharpness": 4 + i%2}}, TotalPrice: 80_000_000 + int64(i)*100_000, SoldAt: now.Add(-time.Duration(i) * time.Minute)}})
	}
	v, ok := e.Valuation("minecraft:netherite_sword")
	if !ok || v.FallbackLevel != "base" || v.ConfidenceBPS <= 0 || v.ConfidenceBPS >= 9900 {
		t.Fatalf("base fallback missing or unpenalized: %+v", v)
	}
}

func TestEngineRanksOnlyQualifiedActiveOpportunities(t *testing.T) {
	e := NewEngine()
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }
	for i := 0; i < 12; i++ {
		e.AddTransactions([]Transaction{{
			SellerName: fmt.Sprintf("seller-%d", i), Item: Item{ID: "diamond", Quantity: 1},
			TotalPrice: 10_000_000 + int64(i%3)*10_000, SoldAt: now.Add(-time.Duration(i) * time.Minute),
		}})
	}
	e.Observe(Listing{AuthoritativeID: "best", SellerName: "best-seller", Item: Item{ID: "diamond", Quantity: 1}, TotalPrice: 2_000_000})
	e.Observe(Listing{AuthoritativeID: "second", SellerName: "second-seller", Item: Item{ID: "diamond", Quantity: 1}, TotalPrice: 4_000_000})
	e.Observe(Listing{AuthoritativeID: "too-expensive", SellerName: "ordinary", Item: Item{ID: "diamond", Quantity: 1}, TotalPrice: 9_900_000})

	got := e.Opportunities(Thresholds{MinProfit: 1_000_000, MinMarginBPS: 1_000, MaxPurchasePrice: 20_000_000, MinVolume24h: 1}, 10)
	if len(got) != 1 || got[0].Listing.AuthoritativeID != "best" {
		t.Fatalf("unexpected ranked opportunities: %+v", got)
	}
	unlimited := e.Opportunities(Thresholds{MinProfit: 1_000_000, MinMarginBPS: 1_000, MaxPurchasePrice: 0, MinVolume24h: 1}, 10)
	if len(unlimited) != 1 || unlimited[0].Listing.AuthoritativeID != "best" {
		t.Fatalf("zero budget cap must mean unlimited: %+v", unlimited)
	}
	_, report := e.AnalyzeOpportunities(Thresholds{MinProfit: 1_000_000, MinMarginBPS: 1_000, MinVolume24h: 1}, 10)
	if report.Listings != 3 || report.LowProfit != 1 || report.DuplicateSignature != 1 || report.Qualified != 1 || report.Published != 1 {
		t.Fatalf("unexpected rejection report: %+v", report)
	}
}

func BenchmarkObserveExistingMarket(b *testing.B) {
	e := NewEngine()
	now := time.Now().UTC()
	ts := make([]Transaction, 100)
	for i := range ts {
		ts[i] = Transaction{SellerName: fmt.Sprint(i), Item: Item{ID: "elytra", Quantity: 1}, TotalPrice: 300_000_000 + int64(i), SoldAt: now}
	}
	e.AddTransactions(ts)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		e.Observe(Listing{AuthoritativeID: "benchmark-listing", SellerName: "benchmark-seller",
			Item: Item{ID: "elytra", Quantity: 1}, TotalPrice: 280_000_000 + int64(i%100)})
	}
}
