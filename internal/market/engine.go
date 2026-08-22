package market

import (
	"math"
	"sort"
	"sync"
	"time"
)

const activeListingFallbackTTL = 2 * time.Minute
const transactionRetention = 31 * 24 * time.Hour

type Snapshot struct {
	Version     uint64               `json:"version"`
	GeneratedAt time.Time            `json:"generated_at"`
	Valuations  map[string]Valuation `json:"valuations"`
}

type PriceUpdate struct {
	Version     uint64    `json:"version"`
	GeneratedAt time.Time `json:"generated_at"`
	Valuation   Valuation `json:"valuation"`
}

type ClientValue struct {
	FairValue      int64 `json:"fair_value"`
	QuickSellValue int64 `json:"quick_sell_value"`
	ConfidenceBPS  int   `json:"confidence_bps"`
	Volume24h      int   `json:"volume_24h"`
}

type ClientSnapshot struct {
	Version     uint64                 `json:"version"`
	GeneratedAt time.Time              `json:"generated_at"`
	Values      map[string]ClientValue `json:"values"`
}

type SnapshotChunk struct {
	Version     uint64               `json:"version"`
	GeneratedAt time.Time            `json:"generated_at"`
	Index       int                  `json:"index"`
	Count       int                  `json:"count"`
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
	l, duplicate, previousSignature := e.observeLocked(raw, now)
	if previousSignature != "" && previousSignature != l.Signature.Exact {
		e.refreshActiveValuationLocked(previousSignature)
	}
	e.refreshActiveValuationMaybeLocked(l.Signature, l.UnitPrice, now)
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
		listing, duplicate, previousSignature := e.observeLocked(raw, now)
		out = append(out, listing)
		if duplicate {
			duplicates++
		}
		touchedSignatures[listing.Signature.Exact] = struct{}{}
		touchedBases[listing.Signature.Base] = struct{}{}
		if previousSignature != "" && previousSignature != listing.Signature.Exact {
			touchedSignatures[previousSignature] = struct{}{}
		}
	}
	for signature := range touchedSignatures {
		e.recalculateLocked(signature)
	}
	for base := range touchedBases {
		e.recalculateBaseLocked(base)
	}
	return out, duplicates
}

func (e *Engine) observeLocked(raw Listing, now time.Time) (Listing, bool, string) {
	l := NormalizeListing(raw)
	if l.FirstSeen.IsZero() {
		l.FirstSeen = now
	}
	l.LastSeen = now
	if old, ok := e.listings[l.Fingerprint]; ok {
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
		return l, true, old.Signature.Exact
	}
	l.ObserverCount = max(1, l.ObserverCount)
	e.listings[l.Fingerprint] = l
	e.putActiveLocked(l, true)
	return l, false, ""
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
		e.refreshActiveValuationLocked(signature)
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
	hasExactEvidence := len(e.transactions[signature.Exact]) >= 3
	hasBaseEvidence := signature.Base != signature.Exact && len(e.baseTransactions[signature.Base]) >= 3
	if !hasExactEvidence && !hasBaseEvidence {
		return
	}
	urgent := false
	if valuation, ok := e.valuations[signature.Exact]; !ok {
		urgent = hasExactEvidence
	} else {
		urgent = valuation.ActiveReferenceAsk == 0 || (unitPrice > 0 && unitPrice <= valuation.ActiveReferenceAsk)
	}
	if !urgent && now.Sub(e.lastActiveRecalc[signature.Exact]) < 250*time.Millisecond {
		return
	}
	e.lastActiveRecalc[signature.Exact] = now
	e.refreshActiveValuationLocked(signature.Exact)
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
func (e *Engine) ClientSnapshot() ClientSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	values := make(map[string]ClientValue, len(e.valuations))
	for signature, valuation := range e.valuations {
		values[signature] = ClientValue{
			FairValue: valuation.FairValue, QuickSellValue: valuation.QuickSellValue,
			ConfidenceBPS: valuation.ConfidenceBPS, Volume24h: valuation.Volume24h,
		}
	}
	generatedAt := e.updatedAt
	if generatedAt.IsZero() {
		generatedAt = e.now()
	}
	return ClientSnapshot{Version: e.version, GeneratedAt: generatedAt, Values: values}
}
func (e *Engine) Version() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.version
}
func (e *Engine) PriceUpdate(signature string) (PriceUpdate, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	valuation, ok := e.valuations[signature]
	generatedAt := e.updatedAt
	if generatedAt.IsZero() {
		generatedAt = e.now()
	}
	return PriceUpdate{Version: e.version, GeneratedAt: generatedAt, Valuation: valuation}, ok
}
func (e *Engine) Valuation(signature string) (Valuation, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	v, ok := e.valuations[signature]
	return v, ok
}
func (e *Engine) Listings(limit int) []Listing {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Listing, 0, len(e.listings))
	for _, l := range e.listings {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Opportunities ranks active listings against conservative quick-sell values.
// It deliberately uses completed-sale confidence and volume gates; active asks
// alone can never create a client alert.
func (e *Engine) Opportunities(thresholds Thresholds, limit int) []Opportunity {
	e.mu.RLock()
	defer e.mu.RUnlock()
	now := e.now()
	out := make([]Opportunity, 0)
	for _, listing := range e.listings {
		if listing.TotalPrice <= 0 || listing.TotalPrice > thresholds.MaxPurchasePrice {
			continue
		}
		deadline := listing.ExpiresAt
		if deadline.IsZero() {
			deadline = listing.LastSeen.Add(activeListingFallbackTTL)
		}
		if !deadline.After(now) {
			continue
		}
		valuation, ok := e.valuations[listing.Signature.Exact]
		if !ok {
			valuation, ok = e.valuations[listing.Signature.Base]
		}
		if !ok || valuation.QuickSellValue <= 0 || valuation.ConfidenceBPS < thresholds.MinConfidenceBPS || valuation.Volume24h < thresholds.MinVolume24h {
			continue
		}
		if opportunityRiskBlocked(valuation.RiskFlags) {
			continue
		}
		quantity := int64(max(1, listing.Item.Quantity))
		if valuation.QuickSellValue > math.MaxInt64/quantity {
			continue
		}
		reference := valuation.QuickSellValue * quantity
		profit := reference - listing.TotalPrice
		margin := opportunityMarginBPS(profit, listing.TotalPrice)
		if profit < thresholds.MinProfit || margin < thresholds.MinMarginBPS {
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
			continue
		}
		seenSignatures[signature] = struct{}{}
		diverse = append(diverse, opportunity)
	}
	out = diverse
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func opportunityRiskBlocked(flags []string) bool {
	for _, flag := range flags {
		if flag == "api_modifier_blindspot" || flag == "stale_references" {
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
func (e *Engine) Transactions(limit int) []Transaction {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := []Transaction{}
	for _, ts := range e.transactions {
		out = append(out, ts...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SoldAt.After(out[j].SoldAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
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
