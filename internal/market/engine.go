package market

import (
	"math"
	"sort"
	"sync"
	"time"
)

const activeListingFallbackTTL = 2 * time.Minute
const activeValuationRefreshInterval = 5 * time.Second
const transactionRetention = 31 * 24 * time.Hour
const QuantityValuationModelVersion = "robust-v6-clearing-price-quantity"

type Snapshot struct {
	Version     uint64               `json:"version"`
	GeneratedAt time.Time            `json:"generated_at"`
	Valuations  map[string]Valuation `json:"valuations"`
}

type Engine struct {
	mu                   sync.RWMutex
	transactions         map[string][]Transaction
	baseTransactions     map[string][]Transaction
	transactionKeys      map[string]time.Time
	listings             map[string]Listing
	activeBySignature    map[string]map[string]Listing
	activeStats          map[string]activeStat
	valuations           map[string]Valuation
	version              uint64
	updatedAt            time.Time
	now                  func() time.Time
	lastActiveSweep      time.Time
	lastTransactionSweep time.Time
	lastActiveRecalc     map[string]time.Time
}

type activeStat struct {
	depth   int
	bestAsk int64
}

type quantityValuationKey struct {
	signature string
	quantity  int
	base      bool
	pair      bool
}

type quantityValuationResult struct {
	valuation Valuation
	ok        bool
}

func NewEngine() *Engine {
	return &Engine{transactions: map[string][]Transaction{}, baseTransactions: map[string][]Transaction{}, transactionKeys: map[string]time.Time{}, listings: map[string]Listing{}, activeBySignature: map[string]map[string]Listing{}, activeStats: map[string]activeStat{}, valuations: map[string]Valuation{}, lastActiveRecalc: map[string]time.Time{}, now: func() time.Time { return time.Now().UTC() }}
}

func (e *Engine) AddTransactions(ts []Transaction) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	seen := map[string]bool{}
	seenBases := map[string]bool{}
	exactBases := map[string]string{}
	added := 0
	now := e.now()
	if e.lastTransactionSweep.IsZero() || now.Sub(e.lastTransactionSweep) >= time.Hour {
		prunedExact, prunedBases := e.pruneTransactionsLocked(now)
		for signature := range prunedExact {
			seen[signature] = true
		}
		for base := range prunedBases {
			seenBases[base] = true
		}
		e.lastTransactionSweep = now
	}
	for _, raw := range ts {
		t := NormalizeTransaction(raw)
		key := t.Signature.Exact
		transactionKey := t.Fingerprint + "/" + t.SoldAt.UTC().Format(time.RFC3339Nano)
		if _, duplicate := e.transactionKeys[transactionKey]; duplicate {
			continue
		}
		e.transactionKeys[transactionKey] = t.SoldAt
		e.transactions[key] = append(e.transactions[key], t)
		e.baseTransactions[t.Signature.Base] = append(e.baseTransactions[t.Signature.Base], t)
		seen[key] = true
		seenBases[t.Signature.Base] = true
		exactBases[key] = t.Signature.Base
		added++
	}
	for key := range seen {
		if key != exactBases[key] {
			e.recalculateLocked(key)
		}
	}
	for base := range seenBases {
		e.recalculateBaseLocked(base)
	}
	return added
}

func (e *Engine) pruneTransactionsLocked(now time.Time) (map[string]struct{}, map[string]struct{}) {
	cutoff := now.Add(-transactionRetention)
	touchedExact := map[string]struct{}{}
	touchedBases := map[string]struct{}{}
	for signature, transactions := range e.transactions {
		kept := transactions[:0]
		for _, transaction := range transactions {
			if transaction.SoldAt.Before(cutoff) {
				touchedExact[signature] = struct{}{}
				touchedBases[transaction.Signature.Base] = struct{}{}
				continue
			}
			kept = append(kept, transaction)
		}
		if len(kept) == 0 {
			delete(e.transactions, signature)
		} else {
			e.transactions[signature] = kept
		}
	}
	if len(touchedExact) > 0 {
		e.baseTransactions = map[string][]Transaction{}
		for _, transactions := range e.transactions {
			for _, transaction := range transactions {
				e.baseTransactions[transaction.Signature.Base] = append(e.baseTransactions[transaction.Signature.Base], transaction)
			}
		}
	}
	for key, soldAt := range e.transactionKeys {
		if soldAt.Before(cutoff) {
			delete(e.transactionKeys, key)
		}
	}
	return touchedExact, touchedBases
}

func (e *Engine) recalculateBaseLocked(base string) {
	if len(e.baseTransactions[base]) < 3 {
		e.deleteValuationLocked(base)
		return
	}
	v, ok := CalculateValuation(ValuationInput{Signature: base, BaseSignature: base, Transactions: e.baseTransactions[base], ActiveListings: e.activeListingsForBaseLocked(base), Now: e.now()})
	if !ok {
		e.deleteValuationLocked(base)
		return
	}
	if len(e.transactions[base]) != len(e.baseTransactions[base]) {
		v.FallbackLevel = "base"
		v.ConfidenceBPS = v.ConfidenceBPS * 8 / 10
	}
	e.version++
	e.updatedAt = v.GeneratedAt
	e.valuations[base] = v
}

func (e *Engine) Observe(raw Listing) (Listing, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	if e.lastActiveSweep.IsZero() || now.Sub(e.lastActiveSweep) >= 5*time.Second {
		e.expireLocked(now)
		e.lastActiveSweep = now
	}
	l, duplicate, previousSignature, changed := e.observeLocked(raw, now)
	if previousSignature != "" && previousSignature != l.Signature.Exact {
		e.refreshActiveValuationLocked(previousSignature)
	}
	if changed && duplicate {
		e.lastActiveRecalc[l.Signature.Exact] = now
		e.refreshActiveValuationLocked(l.Signature.Exact)
	} else if changed {
		e.refreshActiveValuationMaybeLocked(l.Signature, l.UnitPrice, now)
	}
	return l, duplicate
}

func (e *Engine) ObserveBatch(rawListings []Listing) ([]Listing, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	if e.lastActiveSweep.IsZero() || now.Sub(e.lastActiveSweep) >= 5*time.Second {
		e.expireLocked(now)
		e.lastActiveSweep = now
	}
	out := make([]Listing, 0, len(rawListings))
	touchedSignatures := map[string]struct{}{}
	touchedBases := map[string]struct{}{}
	duplicates := 0
	for _, raw := range rawListings {
		listing, duplicate, previousSignature, changed := e.observeLocked(raw, now)
		out = append(out, listing)
		if duplicate {
			duplicates++
		}
		if !changed {
			continue
		}
		if !duplicate && !e.activeValuationRefreshDueLocked(listing.Signature, listing.UnitPrice, now) {
			continue
		}
		touchedSignatures[listing.Signature.Exact] = struct{}{}
		touchedBases[listing.Signature.Base] = struct{}{}
		if previousSignature != "" && previousSignature != listing.Signature.Exact {
			touchedSignatures[previousSignature] = struct{}{}
		}
	}
	for signature := range touchedSignatures {
		e.lastActiveRecalc[signature] = now
		e.recalculateLocked(signature)
	}
	for base := range touchedBases {
		e.recalculateBaseLocked(base)
	}
	return out, duplicates
}

// ObserveFastBatch updates the live active book without rebuilding the shared
// broad/debug valuation map. The newest-page lane evaluates the returned rows
// directly, and order candidates calculate exact quantities from this current
// engine on demand. Recomputing the same large completed-sale cohorts here made
// ingestion materially slower without changing either decision.
func (e *Engine) ObserveFastBatch(rawListings []Listing) ([]Listing, int, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	if e.lastActiveSweep.IsZero() || now.Sub(e.lastActiveSweep) >= 5*time.Second {
		e.expireWithoutRevaluationLocked(now)
		e.lastActiveSweep = now
	}
	out := make([]Listing, 0, len(rawListings))
	duplicates := 0
	changedAny := false
	for _, raw := range rawListings {
		listing, duplicate, _, changed := e.observeLocked(raw, now)
		out = append(out, listing)
		if duplicate {
			duplicates++
		}
		changedAny = changedAny || changed
	}
	return out, duplicates, changedAny
}

func (e *Engine) observeLocked(raw Listing, now time.Time) (Listing, bool, string, bool) {
	l := NormalizeListing(raw)
	if l.FirstSeen.IsZero() {
		l.FirstSeen = now
	}
	l.LastSeen = now
	if old, ok := e.listings[l.Fingerprint]; ok {
		changed := old.Signature != l.Signature || old.TotalPrice != l.TotalPrice || old.Item.Quantity != l.Item.Quantity ||
			old.SellerUUID != l.SellerUUID || old.SellerName != l.SellerName || !old.ExpiresAt.Equal(l.ExpiresAt)
		l.FirstSeen = old.FirstSeen
		if l.ExpiresAt.IsZero() {
			l.ExpiresAt = old.ExpiresAt
		}
		l.ObserverCount = old.ObserverCount + 1
		if old.Signature.Exact != l.Signature.Exact {
			delete(e.activeBySignature[old.Signature.Exact], old.Fingerprint)
			e.rebuildActiveStatLocked(old.Signature.Exact)
		}
		e.listings[l.Fingerprint] = l
		e.putActiveLocked(l, old.Signature.Exact != l.Signature.Exact)
		return l, true, old.Signature.Exact, changed
	}
	l.ObserverCount = max(1, l.ObserverCount)
	e.listings[l.Fingerprint] = l
	e.putActiveLocked(l, true)
	return l, false, "", true
}

func (e *Engine) SweepExpired(now time.Time) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if now.IsZero() {
		now = e.now()
	}
	e.lastActiveSweep = now
	return e.expireLocked(now)
}

func (e *Engine) expireLocked(now time.Time) int {
	return e.expireListingsLocked(now, true)
}

func (e *Engine) expireWithoutRevaluationLocked(now time.Time) int {
	return e.expireListingsLocked(now, false)
}

func (e *Engine) expireListingsLocked(now time.Time, refreshValuations bool) int {
	touched := map[string]struct{}{}
	expired := 0
	for fingerprint, listing := range e.listings {
		deadline := listing.ExpiresAt
		if deadline.IsZero() {
			deadline = listing.LastSeen.Add(activeListingFallbackTTL)
		}
		if deadline.After(now) {
			continue
		}
		delete(e.listings, fingerprint)
		delete(e.activeBySignature[listing.Signature.Exact], fingerprint)
		touched[listing.Signature.Exact] = struct{}{}
		expired++
	}
	for signature := range touched {
		e.rebuildActiveStatLocked(signature)
		if refreshValuations {
			e.refreshActiveValuationLocked(signature)
		}
	}
	return expired
}

func (e *Engine) RemoveListing(fingerprint string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	l, ok := e.listings[fingerprint]
	if ok {
		delete(e.listings, fingerprint)
		delete(e.activeBySignature[l.Signature.Exact], fingerprint)
		e.rebuildActiveStatLocked(l.Signature.Exact)
		e.refreshActiveValuationLocked(l.Signature.Exact)
	}
	return ok
}

func (e *Engine) recalculateLocked(sig string) {
	if len(e.transactions[sig]) < 3 {
		e.deleteValuationLocked(sig)
		return
	}
	base := ""
	activeMap := e.activeBySignature[sig]
	for _, l := range activeMap {
		base = l.Signature.Base
		break
	}
	if base == "" && len(e.transactions[sig]) > 0 {
		base = e.transactions[sig][0].Signature.Base
	}
	v, ok := CalculateValuation(ValuationInput{Signature: sig, BaseSignature: base, Transactions: e.transactions[sig], ActiveListings: listingValues(e.activeBySignature[sig]), Now: e.now()})
	if !ok {
		e.deleteValuationLocked(sig)
		return
	}
	e.version++
	e.updatedAt = v.GeneratedAt
	e.valuations[sig] = v
}

func (e *Engine) refreshActiveValuationMaybeLocked(signature Signature, unitPrice int64, now time.Time) {
	if !e.activeValuationRefreshDueLocked(signature, unitPrice, now) {
		return
	}
	e.refreshActiveValuationLocked(signature.Exact)
}

func (e *Engine) activeValuationRefreshDueLocked(signature Signature, _ int64, now time.Time) bool {
	hasExactEvidence := len(e.transactions[signature.Exact]) >= 3
	hasBaseEvidence := signature.Base != signature.Exact && len(e.baseTransactions[signature.Base]) >= 3
	if !hasExactEvidence && !hasBaseEvidence {
		return false
	}
	// The fast lane evaluates the supplied newest page directly against the live
	// active book, so rebuilding the shared debug/research valuation for every
	// newly undercutting listing adds no detection safety. Bound those shared-map
	// rebuilds as well; a market-moving listing is still scored immediately by
	// AnalyzeListings and becomes visible to other consumers within five seconds.
	if last := e.lastActiveRecalc[signature.Exact]; !last.IsZero() && now.Sub(last) < activeValuationRefreshInterval {
		return false
	}
	e.lastActiveRecalc[signature.Exact] = now
	return true
}

func (e *Engine) deleteValuationLocked(signature string) {
	if _, exists := e.valuations[signature]; !exists {
		return
	}
	delete(e.valuations, signature)
	e.version++
	e.updatedAt = e.now()
}

func listingValues(byFingerprint map[string]Listing) []Listing {
	out := make([]Listing, 0, len(byFingerprint))
	for _, listing := range byFingerprint {
		out = append(out, listing)
	}
	return out
}

func (e *Engine) refreshActiveValuationLocked(signature string) {
	e.recalculateLocked(signature)
	base := ""
	if listings := e.activeBySignature[signature]; len(listings) > 0 {
		for _, listing := range listings {
			base = listing.Signature.Base
			break
		}
	}
	if base != "" && base != signature {
		e.recalculateBaseLocked(base)
	}
}

func (e *Engine) activeListingsForBaseLocked(base string) []Listing {
	out := make([]Listing, 0)
	for _, listing := range e.listings {
		if listing.Signature.Base == base {
			out = append(out, listing)
		}
	}
	return out
}

func (e *Engine) putActiveLocked(l Listing, isNew bool) {
	byFingerprint := e.activeBySignature[l.Signature.Exact]
	if byFingerprint == nil {
		byFingerprint = map[string]Listing{}
		e.activeBySignature[l.Signature.Exact] = byFingerprint
	}
	byFingerprint[l.Fingerprint] = l
	stat := e.activeStats[l.Signature.Exact]
	if isNew {
		stat.depth++
	}
	if stat.bestAsk == 0 || l.UnitPrice < stat.bestAsk {
		stat.bestAsk = l.UnitPrice
	}
	e.activeStats[l.Signature.Exact] = stat
}

func (e *Engine) rebuildActiveStatLocked(signature string) {
	stat := activeStat{}
	for _, l := range e.activeBySignature[signature] {
		stat.depth++
		if stat.bestAsk == 0 || l.UnitPrice < stat.bestAsk {
			stat.bestAsk = l.UnitPrice
		}
	}
	if stat.depth == 0 {
		delete(e.activeStats, signature)
		delete(e.lastActiveRecalc, signature)
	} else {
		e.activeStats[signature] = stat
	}
}

func (e *Engine) Snapshot() Snapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	vals := make(map[string]Valuation, len(e.valuations))
	for k, v := range e.valuations {
		vals[k] = v
	}
	generatedAt := e.updatedAt
	if generatedAt.IsZero() {
		generatedAt = e.now()
	}
	return Snapshot{Version: e.version, GeneratedAt: generatedAt, Valuations: vals}
}

func (e *Engine) Version() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.version
}

func (e *Engine) Valuation(signature string) (Valuation, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	v, ok := e.valuations[signature]
	return v, ok
}

// ActiveListings returns a detached, deterministic view of listings that are
// still live according to the same expiry rules used by valuation. Callers may
// filter this for narrowly scoped supply lookups without exposing engine maps.
func (e *Engine) ActiveListings() []Listing {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	e.expireLocked(now)
	out := make([]Listing, 0, len(e.listings))
	for _, listing := range e.listings {
		out = append(out, listing)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalPrice != out[j].TotalPrice {
			return out[i].TotalPrice < out[j].TotalPrice
		}
		if !out[i].LastSeen.Equal(out[j].LastSeen) {
			return out[i].LastSeen.After(out[j].LastSeen)
		}
		return out[i].Fingerprint < out[j].Fingerprint
	})
	return out
}

// QuantityValuation returns the same singular-ceiling plus exact-batch model
// used by auction opportunities. Combined order/auction candidates must use
// this instead of the generic per-item snapshot valuation.
func (e *Engine) QuantityValuation(signature string, quantity int) (Valuation, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if quantity < 1 {
		quantity = 1
	}
	return e.quantityValuationLocked(signature, quantity, make(map[quantityValuationKey]quantityValuationResult))
}

// QuantityValuations evaluates only exact batch sizes present in completed-sale
// history, under one read lock and one shared calculation cache. Callers that
// need the best quantity up to a stack cap should use this instead of probing
// every integer separately.
func (e *Engine) QuantityValuations(signature string, maximum int) []Valuation {
	e.mu.RLock()
	defer e.mu.RUnlock()
	maximum = max(1, maximum)
	quantities := map[int]struct{}{}
	collect := func(transactions []Transaction) {
		for _, transaction := range transactions {
			quantity := max(1, transaction.Item.Quantity)
			if quantity <= maximum {
				quantities[quantity] = struct{}{}
			}
		}
	}
	collect(e.transactions[signature])
	base := e.baseSignatureLocked(signature)
	if base != signature {
		collect(e.baseTransactions[base])
	}
	ordered := make([]int, 0, len(quantities))
	for quantity := range quantities {
		ordered = append(ordered, quantity)
	}
	sort.Ints(ordered)
	cache := make(map[quantityValuationKey]quantityValuationResult)
	result := make([]Valuation, 0, len(ordered))
	for _, quantity := range ordered {
		if valuation, ok := e.quantityValuationLocked(signature, quantity, cache); ok {
			result = append(result, valuation)
		}
	}
	return result
}

func (e *Engine) baseSignatureLocked(signature string) string {
	base := signature
	if transactions := e.transactions[signature]; len(transactions) > 0 {
		base = transactions[0].Signature.Base
	} else if listings := e.activeBySignature[signature]; len(listings) > 0 {
		for _, listing := range listings {
			base = listing.Signature.Base
			break
		}
	}
	return base
}

func (e *Engine) quantityValuationLocked(signature string, quantity int, cache map[quantityValuationKey]quantityValuationResult) (Valuation, bool) {
	base := e.baseSignatureLocked(signature)
	if valuation, ok := e.quantityPairValuationLocked(signature, base, quantity, false, cache); ok {
		return valuation, true
	}
	if base != signature {
		return e.quantityPairValuationLocked(base, base, quantity, true, cache)
	}
	return Valuation{}, false
}

// Opportunities ranks active listings against conservative quick-sell values.
// It deliberately uses completed-sale confidence and volume gates; active asks
// alone can never create a client alert.
func (e *Engine) Opportunities(thresholds Thresholds, limit int) []Opportunity {
	opportunities, _ := e.AnalyzeOpportunities(thresholds, limit)
	return opportunities
}

func (e *Engine) AnalyzeOpportunities(thresholds Thresholds, limit int) ([]Opportunity, OpportunityReport) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.analyzeListingsLocked(listingValues(e.listings), thresholds, limit)
}

// AnalyzeListings evaluates only the supplied active-book slice against the
// engine's completed-sale model. The subsecond newest-page lane uses this to
// avoid rescoring the entire broad auction window on every API poll.
func (e *Engine) AnalyzeListings(listings []Listing, thresholds Thresholds, limit int) ([]Opportunity, OpportunityReport) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.analyzeListingsLocked(listings, thresholds, limit)
}

func (e *Engine) analyzeListingsLocked(listings []Listing, thresholds Thresholds, limit int) ([]Opportunity, OpportunityReport) {
	now := e.now()
	out := make([]Opportunity, 0)
	report := OpportunityReport{Listings: len(listings)}
	quantityCache := make(map[quantityValuationKey]quantityValuationResult)
	for _, listing := range listings {
		if listing.TotalPrice <= 0 {
			report.InvalidPrice++
			continue
		}
		if thresholds.MaxPurchasePrice > 0 && listing.TotalPrice > thresholds.MaxPurchasePrice {
			report.OverBudget++
			continue
		}
		deadline := listing.ExpiresAt
		if deadline.IsZero() {
			deadline = listing.LastSeen.Add(activeListingFallbackTTL)
		}
		if !deadline.After(now) {
			report.Expired++
			continue
		}
		valuation, ok := e.opportunityValuationLocked(listing, quantityCache)
		if !ok || valuation.QuickSellValue <= 0 {
			if listing.Item.Quantity > 1 {
				report.NoQuantityEvidence++
			} else {
				report.NoValuation++
			}
			continue
		}
		if valuation.Volume24h < thresholds.MinVolume24h {
			report.LowVolume++
			continue
		}
		if valuation.ConfidenceBPS < thresholds.MinConfidenceBPS {
			report.LowConfidence++
			continue
		}
		if opportunityRiskBlocked(valuation.RiskFlags) {
			report.RiskBlocked++
			continue
		}
		quantity := int64(max(1, listing.Item.Quantity))
		if valuation.QuickSellValue > math.MaxInt64/quantity {
			report.Overflow++
			continue
		}
		reference := valuation.QuickSellValue * quantity
		profit := reference - listing.TotalPrice
		margin := opportunityMarginBPS(profit, listing.TotalPrice)
		if profit < thresholds.MinProfit {
			report.LowProfit++
			continue
		}
		if margin < thresholds.MinMarginBPS {
			report.LowMargin++
			continue
		}
		out = append(out, Opportunity{Listing: listing, Valuation: valuation, Profit: profit, MarginBPS: margin})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Profit == out[j].Profit {
			if out[i].MarginBPS == out[j].MarginBPS {
				return out[i].Listing.LastSeen.After(out[j].Listing.LastSeen)
			}
			return out[i].MarginBPS > out[j].MarginBPS
		}
		return out[i].Profit > out[j].Profit
	})
	// One alert opens one filtered auction search. Repeating the same exact item
	// would add chat noise without helping the user reach a different surface.
	diverse := make([]Opportunity, 0, len(out))
	seenSignatures := make(map[string]struct{}, len(out))
	for _, opportunity := range out {
		signature := opportunity.Listing.Signature.Exact
		if _, duplicate := seenSignatures[signature]; duplicate {
			report.DuplicateSignature++
			continue
		}
		seenSignatures[signature] = struct{}{}
		diverse = append(diverse, opportunity)
	}
	out = diverse
	report.Qualified = len(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	report.Published = len(out)
	return out, report
}

// opportunityValuationLocked enforces the executable resale quantity. Every
// listing is anchored to completed quantity=1 sales. Stacks additionally need
// completed sales at the exact listed quantity, and the lower per-unit value
// wins. This prevents both accidental total-price multiplication and profits
// that exist only if the buyer breaks a stack apart before relisting it.
func (e *Engine) opportunityValuationLocked(listing Listing, cache map[quantityValuationKey]quantityValuationResult) (Valuation, bool) {
	quantity := max(1, listing.Item.Quantity)
	if valuation, ok := e.quantityPairValuationLocked(listing.Signature.Exact, listing.Signature.Base, quantity, false, cache); ok {
		return valuation, true
	}
	if listing.Signature.Base != listing.Signature.Exact {
		return e.quantityPairValuationLocked(listing.Signature.Base, listing.Signature.Base, quantity, true, cache)
	}
	return Valuation{}, false
}

func (e *Engine) quantityPairValuationLocked(signature, base string, quantity int, baseFallback bool, cache map[quantityValuationKey]quantityValuationResult) (Valuation, bool) {
	pairKey := quantityValuationKey{signature: signature, quantity: quantity, base: baseFallback, pair: true}
	if cached, exists := cache[pairKey]; exists {
		return cached.valuation, cached.ok
	}
	singular, ok := e.quantityCohortValuationLocked(signature, base, 1, baseFallback, cache)
	if !ok {
		cache[pairKey] = quantityValuationResult{}
		return Valuation{}, false
	}
	if quantity == 1 {
		singular.SingularQuickSell = singular.QuickSellValue
		singular.QuantityQuickSell = singular.QuickSellValue
		singular.PricingQuantity = 1
		singular.SingularVolume24h = singular.Volume24h
		singular.QuantityVolume24h = singular.Volume24h
		cache[pairKey] = quantityValuationResult{valuation: singular, ok: true}
		return singular, true
	}
	stacked, ok := e.quantityCohortValuationLocked(signature, base, quantity, baseFallback, cache)
	if !ok {
		cache[pairKey] = quantityValuationResult{}
		return Valuation{}, false
	}
	combined := combineQuantityValuations(singular, stacked, quantity)
	transactions := e.transactions[signature]
	if baseFallback {
		transactions = e.baseTransactions[signature]
	}
	// We always relist the exact acquired batch. Recompute executable demand at
	// the final conservative target using only sales of that same quantity.
	// Singular sales remain a price ceiling; their volume is not executable
	// evidence for a stack that will not be split before resale.
	quantityTransactions := transactionsAtQuantity(transactions, quantity)
	volume, sellers, priceAge := robustPriceLiquidity24h(quantityTransactions, e.now(), combined.PriceBandLow, combined.PriceBandHigh)
	combined.Volume24h = volume
	combined.PriceSellerCount = sellers
	combined.QuantityVolume24h = volume
	combined.PriceReferenceAgeSeconds = priceAge
	combined.RiskFlags = withLiquidityRiskFlags(combined.RiskFlags, volume, sellers)
	cache[pairKey] = quantityValuationResult{valuation: combined, ok: true}
	return combined, true
}

func (e *Engine) quantityCohortValuationLocked(signature, base string, quantity int, baseFallback bool, cache map[quantityValuationKey]quantityValuationResult) (Valuation, bool) {
	key := quantityValuationKey{signature: signature, quantity: quantity, base: baseFallback}
	if cached, exists := cache[key]; exists {
		return cached.valuation, cached.ok
	}
	transactions := e.transactions[signature]
	activeListings := listingValues(e.activeBySignature[signature])
	if baseFallback {
		transactions = e.baseTransactions[signature]
		activeListings = e.activeListingsForBaseLocked(signature)
	}
	transactions = transactionsAtQuantity(transactions, quantity)
	activeListings = listingsAtQuantity(activeListings, quantity)
	valuation, ok := CalculateValuation(ValuationInput{
		Signature: signature, BaseSignature: base, Transactions: transactions,
		ActiveListings: activeListings, Now: e.now(),
	})
	if ok {
		valuation.ModelVersion = QuantityValuationModelVersion
		if baseFallback {
			valuation.FallbackLevel = "base-quantity"
			valuation.ConfidenceBPS = valuation.ConfidenceBPS * 8 / 10
		} else {
			valuation.FallbackLevel = "exact-quantity"
		}
	}
	cache[key] = quantityValuationResult{valuation: valuation, ok: ok}
	return valuation, ok
}

func transactionsAtQuantity(values []Transaction, quantity int) []Transaction {
	out := make([]Transaction, 0, len(values))
	for _, value := range values {
		if max(1, value.Item.Quantity) == quantity {
			out = append(out, value)
		}
	}
	return out
}

func listingsAtQuantity(values []Listing, quantity int) []Listing {
	out := make([]Listing, 0, len(values))
	for _, value := range values {
		if max(1, value.Item.Quantity) == quantity {
			out = append(out, value)
		}
	}
	return out
}

func combineQuantityValuations(singular, stacked Valuation, quantity int) Valuation {
	combined := stacked
	combined.FairValue = min64(singular.FairValue, stacked.FairValue)
	combined.QuickSellValue = min64(singular.QuickSellValue, stacked.QuickSellValue)
	combined.ShortTermValue = min64(singular.ShortTermValue, stacked.ShortTermValue)
	combined.LongTermValue = min64(singular.LongTermValue, stacked.LongTermValue)
	combined.PriceBandLow, combined.PriceBandHigh = targetPriceBand(combined.QuickSellValue)
	// When the exact-stack model sets the lower target, its evidence also owns
	// confidence, volatility, sell time, and risk. Sparse singular evidence is
	// still enough to impose a lower ceiling, but cannot dilute executable stack
	// liquidity when that ceiling is not the selected price.
	if singular.QuickSellValue < stacked.QuickSellValue {
		combined.ConfidenceBPS = min(singular.ConfidenceBPS, stacked.ConfidenceBPS)
		combined.VolatilityBPS = max(singular.VolatilityBPS, stacked.VolatilityBPS)
		combined.ReferenceAgeSeconds = max64(singular.ReferenceAgeSeconds, stacked.ReferenceAgeSeconds)
		combined.RiskFlags = mergeRiskFlags(singular.RiskFlags, stacked.RiskFlags)
	}
	combined.ModelVersion = QuantityValuationModelVersion
	combined.SingularQuickSell = singular.QuickSellValue
	combined.QuantityQuickSell = stacked.QuickSellValue
	combined.PricingQuantity = quantity
	combined.SingularVolume24h = singular.Volume24h
	combined.QuantityVolume24h = stacked.Volume24h
	return combined
}

func withLiquidityRiskFlags(flags []string, volume, sellers int) []string {
	out := make([]string, 0, len(flags)+2)
	for _, flag := range flags {
		if flag != "low_price_liquidity" && flag != "target_price_seller_concentration" {
			out = append(out, flag)
		}
	}
	if volume < 3 {
		out = append(out, "low_price_liquidity")
	}
	if volume >= 2 && sellers < 2 {
		out = append(out, "target_price_seller_concentration")
	}
	sort.Strings(out)
	return out
}

func mergeRiskFlags(groups ...[]string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, group := range groups {
		for _, flag := range group {
			if _, exists := seen[flag]; exists {
				continue
			}
			seen[flag] = struct{}{}
			out = append(out, flag)
		}
	}
	sort.Strings(out)
	return out
}

func opportunityRiskBlocked(flags []string) bool {
	for _, flag := range flags {
		if flag == "api_modifier_blindspot" || flag == "stale_references" || flag == "target_price_seller_concentration" {
			return true
		}
	}
	return false
}

func opportunityMarginBPS(profit, price int64) int {
	if price <= 0 {
		return 0
	}
	ratio := float64(profit) / float64(price) * 10_000
	if ratio > math.MaxInt32 {
		return math.MaxInt32
	}
	if ratio < math.MinInt32 {
		return math.MinInt32
	}
	return int(ratio)
}
func (e *Engine) Explain(signature string) ValuationDebug {
	e.mu.RLock()
	defer e.mu.RUnlock()
	now := e.now()
	transactions := append([]Transaction(nil), e.transactions[signature]...)
	listings := listingValues(e.activeBySignature[signature])
	base := ""
	if len(transactions) > 0 {
		base = transactions[0].Signature.Base
	} else if len(listings) > 0 {
		base = listings[0].Signature.Base
	}
	sort.Slice(transactions, func(i, j int) bool { return transactions[i].SoldAt.After(transactions[j].SoldAt) })
	sort.Slice(listings, func(i, j int) bool { return listings[i].UnitPrice < listings[j].UnitPrice })
	recent := 0
	for _, transaction := range transactions {
		if transaction.UnitPrice > 0 && !transaction.SoldAt.Before(now.Add(-30*24*time.Hour)) {
			recent++
		}
	}
	debug := ValuationDebug{
		Signature: signature, BaseSignature: base, Status: "learning",
		Reason:       "at least three recent comparable sales are required",
		Transactions: transactions, ActiveListings: listings, RecentRawCount: recent, GeneratedAt: now,
	}
	if len(debug.Transactions) > 100 {
		debug.Transactions = debug.Transactions[:100]
	}
	if len(debug.ActiveListings) > 100 {
		debug.ActiveListings = debug.ActiveListings[:100]
	}
	if valuation, ok := e.valuations[signature]; ok {
		copy := valuation
		debug.Status, debug.Reason, debug.Valuation = "ready", "exact modifier-aware valuation available", &copy
		return debug
	}
	if recent == 0 && len(listings) == 0 {
		debug.Status, debug.Reason = "unknown", "signature has not been observed"
		return debug
	}
	if base != "" && base != signature {
		if valuation, ok := e.valuations[base]; ok {
			copy := valuation
			copy.FallbackLevel = "base"
			debug.Status, debug.Reason, debug.Valuation = "fallback", "exact evidence is insufficient; base-item valuation shown with reduced confidence", &copy
			debug.Transactions = append([]Transaction(nil), e.baseTransactions[base]...)
			sort.Slice(debug.Transactions, func(i, j int) bool { return debug.Transactions[i].SoldAt.After(debug.Transactions[j].SoldAt) })
			debug.RecentRawCount = 0
			for _, transaction := range debug.Transactions {
				if transaction.UnitPrice > 0 && !transaction.SoldAt.Before(now.Add(-30*24*time.Hour)) {
					debug.RecentRawCount++
				}
			}
			if len(debug.Transactions) > 100 {
				debug.Transactions = debug.Transactions[:100]
			}
		}
	}
	return debug
}
