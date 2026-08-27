package market

import (
	"testing"
	"time"
)

func TestValuationRejectsOutliers(t *testing.T) {
	now := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	prices := []int64{98, 99, 100, 100, 101, 102, 1, 10_000}
	ts := make([]Transaction, 0, len(prices))
	for i, p := range prices {
		ts = append(ts, Transaction{UnitPrice: p, SoldAt: now.Add(-time.Duration(i) * time.Hour)})
	}
	v, ok := CalculateValuation(ValuationInput{Signature: "x", Transactions: ts, Now: now})
	if !ok {
		t.Fatal("expected valuation")
	}
	if v.FairValue < 98 || v.FairValue > 102 {
		t.Fatalf("outlier distorted fair value: %d", v.FairValue)
	}
	if v.SampleCount != 6 {
		t.Fatalf("expected 6 filtered samples, got %d", v.SampleCount)
	}
}
func TestValuationNeedsEvidence(t *testing.T) {
	_, ok := CalculateValuation(ValuationInput{Transactions: []Transaction{{UnitPrice: 100}, {UnitPrice: 101}}, Now: time.Now()})
	if ok {
		t.Fatal("valuation should be low-confidence/absent below three samples")
	}
}
func TestActiveAskCapsQuickSell(t *testing.T) {
	now := time.Now().UTC()
	ts := []Transaction{}
	for i := 0; i < 10; i++ {
		ts = append(ts, Transaction{UnitPrice: 1000 + int64(i), SoldAt: now})
	}
	v, _ := CalculateValuation(ValuationInput{Transactions: ts, ActiveListings: []Listing{{SellerUUID: "a", UnitPrice: 900}, {SellerUUID: "b", UnitPrice: 900}, {SellerUUID: "c", UnitPrice: 900}}, Now: now})
	if v.QuickSellValue > 891 {
		t.Fatalf("quick sell %d should be capped below active ask", v.QuickSellValue)
	}
}

func TestRepeatedSellerCannotDominateValuation(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	transactions := []Transaction{
		{SellerUUID: "seller-a", UnitPrice: 100, SoldAt: now.Add(-time.Hour)},
		{SellerUUID: "seller-a", UnitPrice: 101, SoldAt: now.Add(-2 * time.Hour)},
		{SellerUUID: "seller-a", UnitPrice: 102, SoldAt: now.Add(-3 * time.Hour)},
		{SellerUUID: "seller-a", UnitPrice: 10_000, SoldAt: now.Add(-4 * time.Hour)},
		{SellerUUID: "seller-b", UnitPrice: 100, SoldAt: now.Add(-5 * time.Hour)},
		{SellerUUID: "seller-c", UnitPrice: 103, SoldAt: now.Add(-6 * time.Hour)},
	}
	v, ok := CalculateValuation(ValuationInput{Signature: "item", Transactions: transactions, Now: now})
	if !ok {
		t.Fatal("expected valuation")
	}
	if v.FairValue > 110 {
		t.Fatalf("single seller distorted fair value: %d", v.FairValue)
	}
	if v.SellerCount != 3 {
		t.Fatalf("expected three sellers, got %d", v.SellerCount)
	}
}

func TestFallingMarketUsesShortTermEstimate(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	transactions := make([]Transaction, 0, 12)
	for i := 0; i < 8; i++ {
		transactions = append(transactions, Transaction{SellerUUID: "old-" + string(rune('a'+i)), UnitPrice: 1_000, SoldAt: now.Add(-time.Duration(8+i) * 24 * time.Hour)})
	}
	for i := 0; i < 4; i++ {
		transactions = append(transactions, Transaction{SellerUUID: "new-" + string(rune('a'+i)), UnitPrice: 700, SoldAt: now.Add(-time.Duration(i+1) * time.Hour)})
	}
	v, ok := CalculateValuation(ValuationInput{Signature: "item", Transactions: transactions, Now: now})
	if !ok {
		t.Fatal("expected valuation")
	}
	if v.FairValue != 700 || v.Regime != "falling" {
		t.Fatalf("expected falling-market cap at 700, got fair=%d regime=%q short=%d long=%d", v.FairValue, v.Regime, v.ShortTermValue, v.LongTermValue)
	}
}

func TestActiveReferenceAskResistsOneBaitListing(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	transactions := make([]Transaction, 0, 6)
	for i := 0; i < 6; i++ {
		transactions = append(transactions, Transaction{SellerUUID: "seller-" + string(rune('a'+i)), UnitPrice: 1_000, SoldAt: now.Add(-time.Duration(i) * time.Hour)})
	}
	listings := []Listing{
		{SellerUUID: "bait", UnitPrice: 10},
		{SellerUUID: "a", UnitPrice: 980},
		{SellerUUID: "b", UnitPrice: 990},
		{SellerUUID: "c", UnitPrice: 1_000},
		{SellerUUID: "d", UnitPrice: 1_010},
	}
	v, ok := CalculateValuation(ValuationInput{Signature: "item", Transactions: transactions, ActiveListings: listings, Now: now})
	if !ok {
		t.Fatal("expected valuation")
	}
	if v.ActiveBestAsk != 10 || v.ActiveReferenceAsk != 980 {
		t.Fatalf("unexpected active book best=%d reference=%d", v.ActiveBestAsk, v.ActiveReferenceAsk)
	}
	if v.QuickSellValue < 900 {
		t.Fatalf("bait listing should not destroy quick-sell estimate: %d", v.QuickSellValue)
	}
}

func TestSingleSellerCannotInflateActiveReference(t *testing.T) {
	in := ValuationInput{ActiveListings: []Listing{
		{SellerUUID: "attacker", UnitPrice: 10}, {SellerUUID: "attacker", UnitPrice: 9_000},
		{SellerUUID: "attacker", UnitPrice: 10_000}, {SellerUUID: "honest-a", UnitPrice: 100},
		{SellerUUID: "honest-b", UnitPrice: 110},
	}}
	best, reference, _, sellers := activeMarket(in)
	if best != 10 || reference != 100 || sellers != 3 {
		t.Fatalf("best=%d reference=%d sellers=%d", best, reference, sellers)
	}
}

func TestVolumeIsMeasuredNearProposedResalePrice(t *testing.T) {
	now := time.Date(2026, 8, 24, 1, 46, 0, 0, time.UTC)
	type sale struct {
		seller string
		price  int64
	}
	sales := []sale{
		{"enzo", 120_000}, {"disco", 249_000}, {"faruq", 400_000}, {"novox", 400_000},
		{"disco", 249_000}, {"sloan", 100_000}, {"booster", 500_000}, {"disco", 215_000},
		{"disco", 213_000}, {"disco", 214_000}, {"void", 190_000}, {"steve", 400_000}, {"luki", 400_000},
	}
	transactions := make([]Transaction, 0, len(sales))
	for index, value := range sales {
		transactions = append(transactions, Transaction{SellerName: value.seller, UnitPrice: value.price,
			SoldAt: now.Add(-time.Duration(index) * time.Minute)})
	}
	listings := []Listing{
		{SellerName: "a", UnitPrice: 300_000}, {SellerName: "b", UnitPrice: 300_000},
		{SellerName: "c", UnitPrice: 1_000_000}, {SellerName: "d", UnitPrice: 1_000_000_000},
	}
	valuation, ok := CalculateValuation(ValuationInput{Signature: "minecraft:music_disc_lava_chicken",
		Transactions: transactions, ActiveListings: listings, Now: now})
	if !ok {
		t.Fatal("expected valuation")
	}
	if valuation.ActiveReferenceAsk != 300_000 || valuation.QuickSellValue > 297_000 {
		t.Fatalf("active competition did not cap the resale target: %+v", valuation)
	}
	if valuation.MarketVolume24h != 11 || valuation.Volume24h != 0 {
		t.Fatalf("unrelated price regimes counted as target liquidity: %+v", valuation)
	}
	if !containsString(valuation.RiskFlags, "low_price_liquidity") {
		t.Fatalf("missing target-price liquidity warning: %+v", valuation)
	}
}

func TestTargetPriceVolumeUsesTenPercentBandAndSellerCap(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	transactions := []Transaction{
		{SellerName: "repeat", UnitPrice: 90, SoldAt: now},
		{SellerName: "repeat", UnitPrice: 100, SoldAt: now},
		{SellerName: "repeat", UnitPrice: 110, SoldAt: now},
		{SellerName: "repeat", UnitPrice: 100, SoldAt: now},
		{SellerName: "other", UnitPrice: 105, SoldAt: now},
		{SellerName: "too-low", UnitPrice: 89, SoldAt: now},
		{SellerName: "too-high", UnitPrice: 111, SoldAt: now},
		{SellerName: "old", UnitPrice: 100, SoldAt: now.Add(-25 * time.Hour)},
	}
	volume, sellers := robustPriceVolume24h(transactions, now, 90, 110)
	if volume != 4 || sellers != 2 {
		t.Fatalf("expected four price-local sales from two sellers after cap, got volume=%d sellers=%d", volume, sellers)
	}
	_, _, age := robustPriceLiquidity24h(transactions, now, 90, 110)
	if age != 0 {
		t.Fatalf("target-price reference age=%d want=0", age)
	}
}

func TestOneSellerCannotQualifyTargetPriceLiquidity(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	transactions := make([]Transaction, 0, 8)
	for index := 0; index < 8; index++ {
		transactions = append(transactions, Transaction{SellerName: "one-seller", UnitPrice: 1_000,
			SoldAt: now.Add(-time.Duration(index) * time.Minute)})
	}
	valuation, ok := CalculateValuation(ValuationInput{Signature: "item", Transactions: transactions, Now: now})
	if !ok {
		t.Fatal("expected valuation")
	}
	if valuation.Volume24h != 3 || valuation.PriceSellerCount != 1 ||
		!containsString(valuation.RiskFlags, "target_price_seller_concentration") {
		t.Fatalf("single-seller target liquidity was not identified: %+v", valuation)
	}
	if !opportunityRiskBlocked(valuation.RiskFlags) {
		t.Fatal("single-seller target-price volume must not qualify an alert")
	}
}

func TestAPIModifierBlindspotReducesConfidence(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	transactions := make([]Transaction, 0, 8)
	for i := 0; i < 8; i++ {
		transactions = append(transactions, Transaction{SellerUUID: string(rune('a' + i)), Item: Item{ID: "minecraft:diamond_sword"}, UnitPrice: 1_000, SoldAt: now.Add(-time.Duration(i) * time.Hour), Source: SourceDonutAPI})
	}
	v, ok := CalculateValuation(ValuationInput{Signature: "minecraft:diamond_sword", Transactions: transactions, Now: now})
	if !ok || !containsString(v.RiskFlags, "api_modifier_blindspot") {
		t.Fatalf("expected API fidelity warning: %+v", v)
	}
}

func TestValuationCarriesExplainabilityFields(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	transactions := []Transaction{
		{SellerUUID: "a", UnitPrice: 100, SoldAt: now.Add(-49 * time.Hour)},
		{SellerUUID: "b", UnitPrice: 101, SoldAt: now.Add(-50 * time.Hour)},
		{SellerUUID: "c", UnitPrice: 102, SoldAt: now.Add(-51 * time.Hour)},
	}
	v, ok := CalculateValuation(ValuationInput{Signature: "item", Transactions: transactions, Now: now})
	if !ok {
		t.Fatal("expected valuation")
	}
	if v.ModelVersion != ValuationModelVersion || v.ReferenceAgeSeconds < int64((48*time.Hour).Seconds()) {
		t.Fatalf("missing model evidence: %#v", v)
	}
	if !containsString(v.RiskFlags, "stale_references") {
		t.Fatalf("expected stale reference flag, got %v", v.RiskFlags)
	}
}

func TestEnginePrunesExpiredTransactionHistory(t *testing.T) {
	clock := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	engine := NewEngine()
	engine.now = func() time.Time { return clock }
	transactions := []Transaction{
		{SellerName: "a", Item: Item{ID: "minecraft:diamond"}, TotalPrice: 100, SoldAt: clock.Add(-30 * 24 * time.Hour)},
		{SellerName: "b", Item: Item{ID: "minecraft:diamond"}, TotalPrice: 101, SoldAt: clock.Add(-30 * 24 * time.Hour)},
		{SellerName: "c", Item: Item{ID: "minecraft:diamond"}, TotalPrice: 102, SoldAt: clock.Add(-30 * 24 * time.Hour)},
	}
	engine.AddTransactions(transactions)
	if _, ok := engine.Valuation("minecraft:diamond"); !ok {
		t.Fatal("expected initial valuation")
	}
	clock = clock.Add(48 * time.Hour)
	engine.AddTransactions(nil)
	if _, ok := engine.Valuation("minecraft:diamond"); ok || len(engine.transactionKeys) != 0 {
		t.Fatalf("expired history retained: valuation=%v keys=%d", ok, len(engine.transactionKeys))
	}
}

func TestCanonicalSignatureBoundsNestedContainers(t *testing.T) {
	item := Item{ID: "minecraft:diamond"}
	for i := 0; i < 1_000; i++ {
		item = Item{ID: "minecraft:shulker_box", Contents: []Item{item}}
	}
	signature := CanonicalSignature(item)
	if len(signature.Exact) > 1_000 {
		t.Fatalf("nested signature grew without bound: %d bytes", len(signature.Exact))
	}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func BenchmarkCalculateValuation(b *testing.B) {
	now := time.Now()
	ts := make([]Transaction, 500)
	for i := range ts {
		ts[i] = Transaction{UnitPrice: 300_000_000 + int64(i%50)*100_000, SoldAt: now.Add(-time.Duration(i) * time.Minute)}
	}
	in := ValuationInput{Signature: "minecraft:elytra", Transactions: ts, Now: now}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		CalculateValuation(in)
	}
}

func BenchmarkObserveBatch(b *testing.B) {
	now := time.Now().UTC()
	transactions := make([]Transaction, 500)
	for i := range transactions {
		transactions[i] = Transaction{SellerName: string(rune('a' + i%20)), Item: Item{ID: "minecraft:diamond"}, TotalPrice: 1_000 + int64(i%30), SoldAt: now.Add(-time.Duration(i) * time.Minute)}
	}
	listings := make([]Listing, 44)
	for i := range listings {
		listings[i] = Listing{AuthoritativeID: string(rune('a' + i)), SellerName: string(rune('A' + i%20)), Item: Item{ID: "minecraft:diamond"}, TotalPrice: 900 + int64(i), ExpiresAt: now.Add(time.Hour)}
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		engine := NewEngine()
		engine.now = func() time.Time { return now }
		engine.AddTransactions(transactions)
		engine.ObserveBatch(listings)
	}
}
