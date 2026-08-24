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
			TotalPrice: 4_000_000 + int64(i%3)*10_000, SoldAt: now.Add(-time.Duration(i) * time.Minute),
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
	if report.Listings != 3 || report.LowProfit != 2 || report.DuplicateSignature != 0 || report.Qualified != 1 || report.Published != 1 {
		t.Fatalf("unexpected rejection report: %+v", report)
	}
}

func TestStackReferenceIsCappedBySingularUnitValue(t *testing.T) {
	e := NewEngine()
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }
	e.AddTransactions(quantitySales(now, "iron_ingot", 1, 100, 8, "single"))
	e.AddTransactions(quantitySales(now, "iron_ingot", 64, 10_000, 8, "stack"))
	e.Observe(Listing{AuthoritativeID: "overpriced-stack", SellerName: "candidate", Item: Item{ID: "iron_ingot", Quantity: 64}, TotalPrice: 9_000})

	opportunities, report := e.AnalyzeOpportunities(Thresholds{MinProfit: 1, MinMarginBPS: 1, MinConfidenceBPS: 1, MinVolume24h: 1}, 10)
	if len(opportunities) != 0 || report.LowProfit != 1 {
		t.Fatalf("stack escaped singular-price cap: opportunities=%+v report=%+v", opportunities, report)
	}
}

func TestStackReferenceUsesExactQuantityDiscount(t *testing.T) {
	e := NewEngine()
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }
	e.AddTransactions(quantitySales(now, "iron_ingot", 1, 100, 8, "single"))
	e.AddTransactions(quantitySales(now, "iron_ingot", 64, 3_200, 8, "stack"))
	e.Observe(Listing{AuthoritativeID: "not-a-flip", SellerName: "candidate-a", Item: Item{ID: "iron_ingot", Quantity: 64}, TotalPrice: 4_000})

	opportunities, _ := e.AnalyzeOpportunities(Thresholds{MinProfit: 1, MinMarginBPS: 1, MinConfidenceBPS: 1, MinVolume24h: 1}, 10)
	if len(opportunities) != 0 {
		t.Fatalf("bulk discount was ignored: %+v", opportunities)
	}

	e = NewEngine()
	e.now = func() time.Time { return now }
	e.AddTransactions(quantitySales(now, "iron_ingot", 1, 100, 8, "single"))
	e.AddTransactions(quantitySales(now, "iron_ingot", 64, 3_200, 8, "stack"))
	e.Observe(Listing{AuthoritativeID: "real-stack-flip", SellerName: "candidate-b", Item: Item{ID: "iron_ingot", Quantity: 64}, TotalPrice: 2_000})
	opportunities, _ = e.AnalyzeOpportunities(Thresholds{MinProfit: 1, MinMarginBPS: 1, MinConfidenceBPS: 1, MinVolume24h: 1}, 10)
	if len(opportunities) != 1 {
		t.Fatalf("same-quantity resale profit was not found: %+v", opportunities)
	}
	reference := opportunities[0].Listing.TotalPrice + opportunities[0].Profit
	if reference > 3_200 || reference > 100*64 {
		t.Fatalf("reference %d exceeds a conservative quantity basis", reference)
	}
	valuation := opportunities[0].Valuation
	if valuation.PricingQuantity != 64 || valuation.QuickSellValue != min64(valuation.SingularQuickSell, valuation.QuantityQuickSell) {
		t.Fatalf("quantity audit values are inconsistent: %+v", valuation)
	}
	if valuation.SingularVolume24h <= 0 || valuation.QuantityVolume24h <= 0 {
		t.Fatalf("quantity evidence volumes are missing: %+v", valuation)
	}
}

func TestStackWithoutBothQuantityCohortsIsRejected(t *testing.T) {
	e := NewEngine()
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }
	e.AddTransactions(quantitySales(now, "iron_ingot", 1, 100, 8, "single"))
	e.Observe(Listing{AuthoritativeID: "unsupported-stack", SellerName: "candidate", Item: Item{ID: "iron_ingot", Quantity: 64}, TotalPrice: 1})

	opportunities, report := e.AnalyzeOpportunities(Thresholds{MinProfit: 1, MinMarginBPS: 1, MinConfidenceBPS: 1, MinVolume24h: 1}, 10)
	if len(opportunities) != 0 || report.NoQuantityEvidence != 1 {
		t.Fatalf("stack without same-quantity sales was accepted: opportunities=%+v report=%+v", opportunities, report)
	}
}

func TestStackReferenceUsesExactQuantityActiveCompetition(t *testing.T) {
	e := NewEngine()
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }
	e.AddTransactions(quantitySales(now, "iron_ingot", 1, 100, 8, "single"))
	e.AddTransactions(quantitySales(now, "iron_ingot", 64, 3_200, 8, "stack"))
	for index := 0; index < 3; index++ {
		e.Observe(Listing{AuthoritativeID: fmt.Sprintf("competitor-%d", index), SellerName: fmt.Sprintf("competitor-%d", index), Item: Item{ID: "iron_ingot", Quantity: 64}, TotalPrice: 3_200})
	}
	e.Observe(Listing{AuthoritativeID: "candidate", SellerName: "candidate", Item: Item{ID: "iron_ingot", Quantity: 64}, TotalPrice: 1_000})

	opportunities := e.Opportunities(Thresholds{MinProfit: 1, MinMarginBPS: 1, MinConfidenceBPS: 1, MinVolume24h: 1}, 10)
	if len(opportunities) != 1 || opportunities[0].Listing.AuthoritativeID != "candidate" {
		t.Fatalf("unexpected active-competition opportunities: %+v", opportunities)
	}
	reference := opportunities[0].Listing.TotalPrice + opportunities[0].Profit
	if reference > 3_200 {
		t.Fatalf("same-quantity active market did not cap reference: %d", reference)
	}
}

func quantitySales(now time.Time, itemID string, quantity int, totalPrice int64, count int, sellerPrefix string) []Transaction {
	transactions := make([]Transaction, 0, count)
	for index := 0; index < count; index++ {
		transactions = append(transactions, Transaction{
			SellerName: fmt.Sprintf("%s-%d", sellerPrefix, index),
			Item:       Item{ID: itemID, Quantity: quantity}, TotalPrice: totalPrice,
			SoldAt: now.Add(-time.Duration(index) * time.Minute), Source: SourceDonutAPI,
		})
	}
	return transactions
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

func BenchmarkAnalyzeQuantityAwareOpportunities(b *testing.B) {
	e := NewEngine()
	now := time.Now().UTC()
	e.now = func() time.Time { return now }
	e.AddTransactions(quantitySales(now, "iron_ingot", 1, 10_000, 24, "single"))
	e.AddTransactions(quantitySales(now, "iron_ingot", 64, 500_000, 24, "stack"))
	listings := make([]Listing, 1_000)
	for index := range listings {
		listings[index] = Listing{AuthoritativeID: fmt.Sprintf("listing-%d", index), SellerName: fmt.Sprintf("seller-%d", index), Item: Item{ID: "iron_ingot", Quantity: 64}, TotalPrice: 100_000 + int64(index)}
	}
	e.ObserveBatch(listings)
	thresholds := Thresholds{MinProfit: 1, MinMarginBPS: 1, MinConfidenceBPS: 1, MinVolume24h: 1}
	b.ResetTimer()
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		e.AnalyzeOpportunities(thresholds, 100)
	}
}
