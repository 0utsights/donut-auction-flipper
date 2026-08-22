package worker

import (
	"donut-network/internal/market"
	"hash/fnv"
	"sync/atomic"
	"time"
)

type immutableSnapshot struct {
	version     uint64
	generatedAt time.Time
	values      map[string]market.Valuation
}
type Cache struct {
	current    atomic.Pointer[immutableSnapshot]
	staleAfter time.Duration
}

func NewCache(staleAfter time.Duration) *Cache {
	c := &Cache{staleAfter: staleAfter}
	c.current.Store(&immutableSnapshot{values: map[string]market.Valuation{}})
	return c
}
func (c *Cache) Replace(s market.Snapshot) bool {
	old := c.current.Load()
	if old != nil && s.Version <= old.version {
		return false
	}
	copyValues := make(map[string]market.Valuation, len(s.Valuations))
	for k, v := range s.Valuations {
		copyValues[k] = v
	}
	c.current.Store(&immutableSnapshot{version: s.Version, generatedAt: s.GeneratedAt, values: copyValues})
	return true
}
func (c *Cache) Apply(update market.PriceUpdate) bool {
	for {
		old := c.current.Load()
		if update.Version <= old.version {
			return false
		}
		values := make(map[string]market.Valuation, len(old.values)+1)
		for k, v := range old.values {
			values[k] = v
		}
		values[update.Valuation.Signature] = update.Valuation
		next := &immutableSnapshot{version: update.Version, generatedAt: update.GeneratedAt, values: values}
		if c.current.CompareAndSwap(old, next) {
			return true
		}
	}
}
func (c *Cache) Invalidate(version uint64, generatedAt time.Time, signature string) bool {
	for {
		old := c.current.Load()
		if version <= old.version {
			return false
		}
		values := make(map[string]market.Valuation, len(old.values))
		for k, v := range old.values {
			if k != signature {
				values[k] = v
			}
		}
		next := &immutableSnapshot{version: version, generatedAt: generatedAt, values: values}
		if c.current.CompareAndSwap(old, next) {
			return true
		}
	}
}
func (c *Cache) Version() uint64 { return c.current.Load().version }
func (c *Cache) Stale(now time.Time) bool {
	s := c.current.Load()
	return s.generatedAt.IsZero() || now.Sub(s.generatedAt) > c.staleAfter
}

func (c *Cache) Evaluate(l market.Listing, t market.Thresholds) (market.Opportunity, bool) {
	start := time.Now()
	snapshot := c.current.Load()
	v, ok := snapshot.values[l.Signature.Exact]
	if !ok && l.Signature.Base != l.Signature.Exact {
		v, ok = snapshot.values[l.Signature.Base]
	}
	if !ok || v.ConfidenceBPS < t.MinConfidenceBPS || v.Volume24h < t.MinVolume24h {
		return market.Opportunity{}, false
	}
	if t.MaxPurchasePrice > 0 && l.TotalPrice > t.MaxPurchasePrice {
		return market.Opportunity{}, false
	}
	profit := v.QuickSellValue*int64(max(1, l.Item.Quantity)) - l.TotalPrice
	if profit < t.MinProfit {
		return market.Opportunity{}, false
	}
	margin := 0
	if l.TotalPrice > 0 {
		margin = int(profit * 10000 / l.TotalPrice)
	}
	if margin < t.MinMarginBPS {
		return market.Opportunity{}, false
	}
	return market.Opportunity{Listing: l, Valuation: v, Profit: profit, MarginBPS: margin, DecisionNS: time.Since(start).Nanoseconds()}, true
}

type PurchaseMode string

const (
	NotifyOnly      PurchaseMode = "notify"
	Assisted        PurchaseMode = "assisted"
	Simulated       PurchaseMode = "simulated"
	LiveInteraction PurchaseMode = "live"
)

type PurchaseRequest struct {
	Listing           market.Listing
	ExpectedSignature string
	ExpectedPrice     int64
	ExpectedSeller    string
}
type PurchaseResult struct {
	Success     bool
	Mode        PurchaseMode
	Reason      string
	CompletedAt time.Time
}
type PurchaseController interface {
	Attempt(PurchaseRequest) (PurchaseResult, error)
}
type SimulatorPurchaseController struct{ SuccessRatePercent int }

func (s SimulatorPurchaseController) Attempt(r PurchaseRequest) (PurchaseResult, error) {
	result := PurchaseResult{Mode: Simulated, CompletedAt: time.Now().UTC()}
	if r.Listing.Signature.Exact != r.ExpectedSignature {
		result.Reason = "signature changed"
		return result, nil
	}
	if r.Listing.TotalPrice != r.ExpectedPrice {
		result.Reason = "price changed"
		return result, nil
	}
	if r.ExpectedSeller != "" && r.Listing.SellerName != r.ExpectedSeller {
		result.Reason = "seller changed"
		return result, nil
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(r.Listing.Fingerprint))
	result.Success = int(h.Sum32()%100) < s.SuccessRatePercent
	if result.Success {
		result.Reason = "simulated purchase accepted"
	} else {
		result.Reason = "simulated competing buyer won"
	}
	return result, nil
}
