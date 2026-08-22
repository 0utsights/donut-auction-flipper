package market

import (
	"math"
	"sort"
	"strings"
	"time"
)

const ValuationModelVersion = "robust-v2"

type ValuationInput struct {
	Signature      string
	BaseSignature  string
	Transactions   []Transaction
	ActiveListings []Listing
	ActiveBestAsk  int64
	ActiveDepth    int
	Now            time.Time
}

func CalculateValuation(in ValuationInput) (Valuation, bool) {
	if in.Now.IsZero() {
		in.Now = time.Now().UTC()
	}
	raw := make([]Transaction, 0, len(in.Transactions))
	for _, t := range in.Transactions {
		if t.UnitPrice > 0 && !t.SoldAt.Before(in.Now.Add(-30*24*time.Hour)) {
			raw = append(raw, t)
		}
	}
	if len(raw) < 3 {
		return Valuation{}, false
	}
	deduplicated, sellerCount, sellerConcentrated := deduplicateSellerDays(raw)
	if len(deduplicated) < 3 {
		// Availability is preferable to pretending the market disappeared, but the
		// confidence penalty below makes seller-concentrated evidence explicit.
		deduplicated = append([]Transaction(nil), raw...)
		sellerConcentrated = true
	}
	filtered := filterMADTransactions(deduplicated)
	if len(filtered) < 3 {
		return Valuation{}, false
	}
	prices := transactionPrices(filtered)
	longTerm := weightedMedianHalfLife(filtered, prices, in.Now, 30*24*time.Hour)
	if longTerm <= 0 {
		longTerm = median(prices)
	}
	shortSamples, shortWindowHours := shortWindow(filtered, in.Now)
	shortTerm := weightedMedian(shortSamples, transactionPrices(shortSamples), in.Now)
	if shortTerm <= 0 {
		shortTerm = longTerm
	}
	if len(shortSamples) >= 12 && shortWindowHours == 24 {
		shortTerm = percentile(transactionPrices(shortSamples), 25)
		shortWindowHours = 24
	}
	fair := min64(shortTerm, longTerm)
	mad := medianAbsoluteDeviation(prices, median(prices))
	volBPS := 0
	if fair > 0 {
		volBPS = ratioInt(mad, fair, 14826)
	}
	bestAsk, referenceAsk, depth, activeSellers := activeMarket(in)
	volume := 0
	volume = robustVolume24h(raw, in.Now)
	newest := filtered[0].SoldAt
	for _, transaction := range filtered[1:] {
		if transaction.SoldAt.After(newest) {
			newest = transaction.SoldAt
		}
	}
	referenceAge := max64(0, int64(in.Now.Sub(newest).Seconds()))
	riskFlags := make([]string, 0, 5)
	regime := "stable"
	if float64(shortTerm) < float64(longTerm)*0.9 {
		regime = "falling"
		riskFlags = append(riskFlags, "falling_market")
	} else if float64(shortTerm) > float64(longTerm)*1.1 {
		regime = "rising"
		riskFlags = append(riskFlags, "rising_market_capped")
	}
	if sellerConcentrated || sellerCount < 3 {
		riskFlags = append(riskFlags, "seller_concentration")
	}
	if volume < 3 {
		riskFlags = append(riskFlags, "low_liquidity")
	}
	if referenceAge > int64((48 * time.Hour).Seconds()) {
		riskFlags = append(riskFlags, "stale_references")
	}
	if volBPS > 1500 {
		riskFlags = append(riskFlags, "high_volatility")
	}
	if hasAPIModifierBlindspot(raw) {
		riskFlags = append(riskFlags, "api_modifier_blindspot")
	}
	confidence := 1200 + min(len(filtered), 20)*140 + min(volume, 30)*90 + min(sellerCount, 8)*250 + min(depth, 10)*60
	confidence -= max(0, volBPS-400) / 2
	confidence -= min(2500, int(referenceAge/3600)*35)
	if sellerConcentrated {
		confidence -= 1400
	}
	if regime == "falling" {
		confidence -= 500
	}
	if hasAPIModifierBlindspot(raw) {
		confidence -= 1200
	}
	confidence = max(0, min(9900, confidence))
	quick := quickSellValue(fair, volBPS, referenceAsk)
	spread := 0
	if bestAsk > 0 && fair > 0 {
		spread = signedRatioInt(bestAsk-fair, fair, 10000)
	}
	expectedSellMinutes := min(30*24*60, max(1, (depth+1)*24*60/max(1, volume)))
	return Valuation{
		Signature: in.Signature, BaseSignature: in.BaseSignature, FairValue: fair,
		QuickSellValue: quick, ShortTermValue: shortTerm, LongTermValue: longTerm,
		ActiveBestAsk: bestAsk, ActiveReferenceAsk: referenceAsk, ActiveDepth: depth,
		ActiveSellerCount: activeSellers, ConfidenceBPS: confidence, Volume24h: volume,
		SampleCount: len(filtered), RawSampleCount: len(raw), SellerCount: sellerCount,
		FreshSampleCount: len(shortSamples), VolatilityBPS: volBPS, SpreadBPS: spread,
		ExpectedSellMinutes: expectedSellMinutes, ReferenceAgeSeconds: referenceAge,
		ShortWindowHours: shortWindowHours, Regime: regime, RiskFlags: riskFlags,
		ModelVersion: ValuationModelVersion, FallbackLevel: "exact", GeneratedAt: in.Now,
	}, true
}

func activeMarket(in ValuationInput) (bestAsk, referenceAsk int64, depth, sellers int) {
	asks := make([]int64, 0, len(in.ActiveListings)+1)
	sellerMinimum := map[string]int64{}
	if in.ActiveBestAsk > 0 {
		asks = append(asks, in.ActiveBestAsk)
		depth = in.ActiveDepth
	}
	for _, listing := range in.ActiveListings {
		if listing.UnitPrice <= 0 {
			continue
		}
		asks = append(asks, listing.UnitPrice)
		if in.ActiveDepth == 0 {
			depth++
		}
		identity := sellerIdentity(listing.SellerUUID, listing.SellerName, listing.Fingerprint)
		if prior := sellerMinimum[identity]; prior == 0 || listing.UnitPrice < prior {
			sellerMinimum[identity] = listing.UnitPrice
		}
	}
	if len(asks) == 0 {
		return 0, 0, depth, len(sellerMinimum)
	}
	sort.Slice(asks, func(i, j int) bool { return asks[i] < asks[j] })
	bestAsk = asks[0]
	if len(sellerMinimum) >= 3 {
		distinctAsks := make([]int64, 0, len(sellerMinimum))
		for _, ask := range sellerMinimum {
			distinctAsks = append(distinctAsks, ask)
		}
		sort.Slice(distinctAsks, func(i, j int) bool { return distinctAsks[i] < distinctAsks[j] })
		// The third cheapest distinct seller ignores one bait listing without
		// allowing a single seller's listing wall to inflate the market cap.
		referenceAsk = distinctAsks[2]
	} else if len(in.ActiveListings) == 0 && in.ActiveBestAsk > 0 {
		referenceAsk = in.ActiveBestAsk
	}
	return bestAsk, referenceAsk, depth, len(sellerMinimum)
}

func quickSellValue(fair int64, volatilityBPS int, activeReferenceAsk int64) int64 {
	quick := scaledPositive(fair, int64(max(8200, 9700-min(volatilityBPS, 1500))), 10000)
	if activeReferenceAsk > 0 && activeReferenceAsk < fair {
		quick = min64(quick, scaledPositive(activeReferenceAsk, 9900, 10000))
	}
	return quick
}

func deduplicateSellerDays(transactions []Transaction) ([]Transaction, int, bool) {
	type group struct{ values []Transaction }
	groups := map[string]*group{}
	sellers := map[string]int{}
	for _, transaction := range transactions {
		identity := sellerIdentity(transaction.SellerUUID, transaction.SellerName, transaction.Fingerprint)
		sellers[identity]++
		day := transaction.SoldAt.UTC().Format("2006-01-02")
		key := identity + "\x00" + day
		if groups[key] == nil {
			groups[key] = &group{}
		}
		groups[key].values = append(groups[key].values, transaction)
	}
	out := make([]Transaction, 0, len(groups))
	for _, bucket := range groups {
		sort.Slice(bucket.values, func(i, j int) bool { return bucket.values[i].UnitPrice < bucket.values[j].UnitPrice })
		out = append(out, bucket.values[len(bucket.values)/2])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SoldAt.After(out[j].SoldAt) })
	maxSeller := 0
	for _, count := range sellers {
		maxSeller = max(maxSeller, count)
	}
	concentrated := len(sellers) < 3 || maxSeller*2 > len(transactions)
	return out, len(sellers), concentrated
}

func sellerIdentity(uuid, name, fallback string) string {
	if normalized := strings.ToLower(strings.TrimSpace(uuid)); normalized != "" {
		return "uuid:" + normalized
	}
	if normalized := strings.ToLower(strings.TrimSpace(name)); normalized != "" {
		return "name:" + normalized
	}
	return "unknown:" + fallback
}

func filterMADTransactions(values []Transaction) []Transaction {
	prices := transactionPrices(values)
	m := median(prices)
	mad := medianAbsoluteDeviation(prices, m)
	if mad == 0 {
		return append([]Transaction(nil), values...)
	}
	limit := mad * 6
	out := make([]Transaction, 0, len(values))
	for _, value := range values {
		if abs64(value.UnitPrice-m) <= limit {
			out = append(out, value)
		}
	}
	return out
}

func transactionPrices(values []Transaction) []int64 {
	out := make([]int64, len(values))
	for i, value := range values {
		out[i] = value.UnitPrice
	}
	return out
}

func shortWindow(values []Transaction, now time.Time) ([]Transaction, int) {
	for _, hours := range []int{24, 72, 24 * 7} {
		cutoff := now.Add(-time.Duration(hours) * time.Hour)
		out := make([]Transaction, 0, len(values))
		for _, value := range values {
			if !value.SoldAt.Before(cutoff) {
				out = append(out, value)
			}
		}
		if len(out) >= 4 {
			return out, hours
		}
	}
	return append([]Transaction(nil), values...), 30 * 24
}

func allWithin(values []Transaction, cutoff time.Time) bool {
	for _, value := range values {
		if value.SoldAt.Before(cutoff) {
			return false
		}
	}
	return true
}

func robustVolume24h(values []Transaction, now time.Time) int {
	perSeller := map[string]int{}
	volume := 0
	cutoff := now.Add(-24 * time.Hour)
	for _, value := range values {
		if value.SoldAt.Before(cutoff) {
			continue
		}
		identity := sellerIdentity(value.SellerUUID, value.SellerName, value.Fingerprint)
		if perSeller[identity] >= 3 {
			continue
		}
		perSeller[identity]++
		volume++
	}
	return volume
}

func hasAPIModifierBlindspot(values []Transaction) bool {
	for _, value := range values {
		if value.Source == SourceDonutAPI && modifierSensitiveItem(value.Item.ID) {
			return true
		}
	}
	return false
}

func modifierSensitiveItem(id string) bool {
	id = strings.ToLower(id)
	for _, suffix := range []string{"_sword", "_pickaxe", "_axe", "_shovel", "_hoe", "_helmet", "_chestplate", "_leggings", "_boots", "bow", "crossbow", "trident", "mace", "elytra", "shield", "fishing_rod", "shears", "flint_and_steel"} {
		if strings.HasSuffix(id, suffix) {
			return true
		}
	}
	return false
}

func percentile(values []int64, percent int) int64 {
	if len(values) == 0 {
		return 0
	}
	v := append([]int64(nil), values...)
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	index := (len(v) - 1) * percent / 100
	return v[index]
}

func filterMAD(values []int64) []int64 {
	m := median(values)
	mad := medianAbsoluteDeviation(values, m)
	if mad == 0 {
		return append([]int64(nil), values...)
	}
	out := make([]int64, 0, len(values))
	limit := mad * 6
	for _, v := range values {
		if abs64(v-m) <= limit {
			out = append(out, v)
		}
	}
	return out
}

func median(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	v := append([]int64(nil), values...)
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	n := len(v)
	if n%2 == 1 {
		return v[n/2]
	}
	return v[n/2-1] + (v[n/2]-v[n/2-1])/2
}

func medianAbsoluteDeviation(values []int64, center int64) int64 {
	d := make([]int64, len(values))
	for i, v := range values {
		d[i] = abs64(v - center)
	}
	return median(d)
}

func weightedMedian(transactions []Transaction, allowed []int64, now time.Time) int64 {
	return weightedMedianHalfLife(transactions, allowed, now, 7*24*time.Hour)
}

func weightedMedianHalfLife(transactions []Transaction, allowed []int64, now time.Time, halfLife time.Duration) int64 {
	allowedCount := map[int64]int{}
	for _, v := range allowed {
		allowedCount[v]++
	}
	type point struct {
		price  int64
		weight float64
	}
	pts := make([]point, 0, len(transactions))
	total := 0.0
	for _, t := range transactions {
		if allowedCount[t.UnitPrice] == 0 {
			continue
		}
		allowedCount[t.UnitPrice]--
		age := now.Sub(t.SoldAt).Hours()
		if age < 0 {
			age = 0
		}
		halfLifeHours := halfLife.Hours()
		if halfLifeHours <= 0 {
			halfLifeHours = 24 * 7
		}
		w := math.Exp(-age / halfLifeHours)
		pts = append(pts, point{t.UnitPrice, w})
		total += w
	}
	if len(pts) == 0 {
		return 0
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].price < pts[j].price })
	c := 0.0
	for _, p := range pts {
		c += p.weight
		if c >= total/2 {
			return p.price
		}
	}
	return pts[len(pts)-1].price
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func scaledPositive(value, multiplier, divisor int64) int64 {
	if value <= 0 || multiplier <= 0 || divisor <= 0 {
		return 0
	}
	return (value/divisor)*multiplier + (value%divisor)*multiplier/divisor
}

func ratioInt(numerator, denominator int64, scale int) int {
	if numerator <= 0 || denominator <= 0 || scale <= 0 {
		return 0
	}
	result := float64(numerator) / float64(denominator) * float64(scale)
	if result > float64(^uint(0)>>1) {
		return int(^uint(0) >> 1)
	}
	return int(result)
}

func signedRatioInt(numerator, denominator int64, scale int) int {
	if denominator <= 0 || scale <= 0 {
		return 0
	}
	result := float64(numerator) / float64(denominator) * float64(scale)
	maxInt := int(^uint(0) >> 1)
	minInt := -maxInt - 1
	if result > float64(maxInt) {
		return maxInt
	}
	if result < float64(minInt) {
		return minInt
	}
	return int(result)
}
