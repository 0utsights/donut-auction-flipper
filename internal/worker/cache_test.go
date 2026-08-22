package worker

import (
	"donut-network/internal/market"
	"testing"
	"time"
)

func TestCacheAtomicVersionAndEvaluate(t *testing.T) {
	c := NewCache(time.Minute)
	v := market.Valuation{Signature: "minecraft:elytra", QuickSellValue: 300, ConfidenceBPS: 9000, Volume24h: 20}
	if !c.Replace(market.Snapshot{Version: 2, GeneratedAt: time.Now(), Valuations: map[string]market.Valuation{v.Signature: v}}) {
		t.Fatal("snapshot not applied")
	}
	if c.Replace(market.Snapshot{Version: 1, GeneratedAt: time.Now()}) {
		t.Fatal("older snapshot replaced current")
	}
	updated := v
	updated.QuickSellValue = 310
	if !c.Apply(market.PriceUpdate{Version: 3, GeneratedAt: time.Now(), Valuation: updated}) {
		t.Fatal("incremental update rejected")
	}
	l := market.NormalizeListing(market.Listing{Item: market.Item{ID: "elytra", Quantity: 1}, TotalPrice: 200})
	op, ok := c.Evaluate(l, market.Thresholds{MinProfit: 50, MinMarginBPS: 1000, MinConfidenceBPS: 5000, MinVolume24h: 1})
	if !ok || op.Profit != 110 {
		t.Fatalf("bad decision: %+v %v", op, ok)
	}
	if !c.Invalidate(4, time.Now(), v.Signature) {
		t.Fatal("invalidation rejected")
	}
	if _, ok := c.Evaluate(l, market.Thresholds{}); ok {
		t.Fatal("invalidated valuation remained available")
	}
	if c.Invalidate(3, time.Now(), v.Signature) {
		t.Fatal("stale invalidation applied")
	}
}
func TestPurchaseRevalidation(t *testing.T) {
	l := market.NormalizeListing(market.Listing{SellerName: "a", Item: market.Item{ID: "elytra"}, TotalPrice: 100})
	c := SimulatorPurchaseController{SuccessRatePercent: 100}
	r, _ := c.Attempt(PurchaseRequest{Listing: l, ExpectedSignature: l.Signature.Exact, ExpectedPrice: 101, ExpectedSeller: "a"})
	if r.Success || r.Reason != "price changed" {
		t.Fatalf("stale price accepted: %+v", r)
	}
}

func TestCacheUsesBaseFallbackForUnseenVariant(t *testing.T) {
	c := NewCache(time.Minute)
	base := market.Valuation{Signature: "minecraft:netherite_sword", QuickSellValue: 300, ConfidenceBPS: 7000, Volume24h: 20, FallbackLevel: "base"}
	c.Replace(market.Snapshot{Version: 1, GeneratedAt: time.Now(), Valuations: map[string]market.Valuation{base.Signature: base}})
	l := market.NormalizeListing(market.Listing{Item: market.Item{ID: "netherite_sword", Quantity: 1, Enchantments: map[string]int{"sharpness": 5}}, TotalPrice: 200})
	op, ok := c.Evaluate(l, market.Thresholds{MinProfit: 50, MinConfidenceBPS: 5000, MinVolume24h: 1})
	if !ok || op.Profit != 100 || op.Valuation.FallbackLevel != "base" {
		t.Fatalf("base fallback not used: %+v ok=%v", op, ok)
	}
}
func BenchmarkCachedListingEvaluation(b *testing.B) {
	c := NewCache(time.Minute)
	v := market.Valuation{Signature: "minecraft:elytra", QuickSellValue: 300_000_000, ConfidenceBPS: 9000, Volume24h: 20}
	c.Replace(market.Snapshot{Version: 1, GeneratedAt: time.Now(), Valuations: map[string]market.Valuation{v.Signature: v}})
	l := market.NormalizeListing(market.Listing{Item: market.Item{ID: "elytra", Quantity: 1}, TotalPrice: 200_000_000})
	thresholds := market.Thresholds{MinProfit: 1_000_000, MinMarginBPS: 100, MinConfidenceBPS: 5000}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = c.Evaluate(l, thresholds)
	}
}
