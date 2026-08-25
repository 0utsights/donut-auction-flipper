package orders

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"donut-network/internal/market"
)

type Config struct {
	DatabasePath   string
	AuctionFeeBPS  int
	OrderFeeBPS    int
	CandidateLimit int
}

type System struct {
	store      *Store
	cfg        Config
	now        func() time.Time
	version    atomic.Uint64
	candidates atomic.Pointer[CandidateFeed]
	refreshMu  sync.Mutex
}

func NewSystem(cfg Config) (*System, error) {
	if cfg.AuctionFeeBPS < 0 || cfg.AuctionFeeBPS > 5_000 {
		return nil, errors.New("auction fee must be between 0 and 5000 bps")
	}
	if cfg.OrderFeeBPS < 0 || cfg.OrderFeeBPS > 5_000 {
		return nil, errors.New("order fee must be between 0 and 5000 bps")
	}
	if cfg.CandidateLimit <= 0 {
		cfg.CandidateLimit = 100
	}
	store, err := OpenStore(cfg.DatabasePath)
	if err != nil {
		return nil, err
	}
	system := &System{store: store, cfg: cfg, now: func() time.Time { return time.Now().UTC() }}
	initial := &CandidateFeed{GeneratedAt: system.now(), Candidates: []Candidate{}}
	system.candidates.Store(initial)
	return system, nil
}

func (s *System) Close() error { return s.store.Close() }
func (s *System) Register(ctx context.Context, value ObserverRegistration) (Observer, error) {
	return s.store.Register(ctx, value)
}
func (s *System) Heartbeat(ctx context.Context, value Heartbeat) error {
	return s.store.Heartbeat(ctx, value)
}
func (s *System) LeaseTask(ctx context.Context, observerID string) (*Task, error) {
	return s.store.LeaseTask(ctx, observerID, 30*time.Second)
}
func (s *System) SaveScan(ctx context.Context, value ScanBatch) (bool, error) {
	return s.store.SaveScan(ctx, value)
}
func (s *System) CompleteTask(ctx context.Context, value TaskResult) error {
	return s.store.CompleteTask(ctx, value)
}
func (s *System) AddWatch(ctx context.Context, signature string) (Watch, error) {
	return s.store.AddWatch(ctx, signature, 5*time.Minute)
}
func (s *System) DeleteWatch(ctx context.Context, id string) error {
	return s.store.DeleteWatch(ctx, id)
}
func (s *System) SaveDiagnostic(ctx context.Context, value Diagnostic) error {
	return s.store.SaveDiagnostic(ctx, value)
}
func (s *System) Cleanup(ctx context.Context) error          { return s.store.Cleanup(ctx) }
func (s *System) Backup(ctx context.Context) (string, error) { return s.store.Backup(ctx) }

func (s *System) CandidateFeed() CandidateFeed {
	current := s.candidates.Load()
	copyFeed := *current
	copyFeed.Candidates = append([]Candidate{}, current.Candidates...)
	return copyFeed
}

func (s *System) Refresh(ctx context.Context, engine *market.Engine) error {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	evidence, err := s.store.Evidence(ctx)
	if err != nil {
		return err
	}
	valuations := make(map[string]market.Valuation, len(evidence))
	for _, item := range evidence {
		quantity := max(1, item.ObservedQuantity)
		valuation, ok := engine.QuantityValuation(item.Signature, quantity)
		if !ok && item.ItemID != item.Signature {
			valuation, ok = engine.QuantityValuation(item.ItemID, quantity)
		}
		if ok {
			valuations[item.Signature] = valuation
		}
	}
	now := s.now()
	candidates := buildCandidates(evidence, valuations, s.cfg, now)
	if len(candidates) > s.cfg.CandidateLimit {
		candidates = candidates[:s.cfg.CandidateLimit]
	}
	version := s.version.Add(1)
	s.candidates.Store(&CandidateFeed{Version: version, GeneratedAt: now, Candidates: candidates})
	return nil
}

func (s *System) Debug(ctx context.Context) (DebugSnapshot, error) {
	debug, err := s.store.Debug(ctx)
	if err != nil {
		return DebugSnapshot{}, err
	}
	debug.Candidates = s.CandidateFeed().Candidates
	debug.ReferencePortfolio = referencePortfolio(debug.Candidates, 10_000_000)
	return debug, nil
}

func referencePortfolio(candidates []Candidate, balance int64) []ReferenceSelection {
	deployable := balance * 7_500 / 10_000
	var cash int64
	ordersUsed, auctionsUsed := 0, 0
	exactExposure, itemExposure := map[string]int64{}, map[string]int64{}
	result := []ReferenceSelection{}
	for _, value := range candidates {
		if value.State != "READY" || value.AcquisitionCost <= 0 || value.RiskAdjustedProfitDay <= 0 {
			continue
		}
		maximum := value.ExecutableBatches
		maximum = min(maximum, int((deployable-cash)/value.AcquisitionCost))
		if value.OrderSlots > 0 {
			maximum = min(maximum, (20-ordersUsed)/value.OrderSlots)
		}
		if value.AuctionSlots > 0 {
			maximum = min(maximum, (18-auctionsUsed)/value.AuctionSlots)
		}
		maximum = min(maximum, int((deployable/4-exactExposure[value.Signature])/value.AcquisitionCost))
		maximum = min(maximum, int((deployable*2/5-itemExposure[value.ItemID])/value.AcquisitionCost))
		if maximum <= 0 {
			continue
		}
		capital := mulMoney(value.AcquisitionCost, int64(maximum))
		cash += capital
		ordersUsed += value.OrderSlots * maximum
		auctionsUsed += value.AuctionSlots * maximum
		exactExposure[value.Signature] += capital
		itemExposure[value.ItemID] += capital
		result = append(result, ReferenceSelection{CandidateID: value.ID, ItemName: value.ItemName, Route: value.Route,
			Batches: maximum, Capital: capital, RiskAdjustedProfitDay: mulMoney(value.RiskAdjustedProfitDay, int64(maximum))})
	}
	return result
}

func buildCandidates(allEvidence []Evidence, valuations map[string]market.Valuation, cfg Config, now time.Time) []Candidate {
	result := make([]Candidate, 0, len(allEvidence)*2)
	for _, evidence := range allEvidence {
		valuation, ok := valuations[evidence.Signature]
		if !ok {
			valuation, ok = valuations[evidence.ItemID]
		}
		if !ok {
			continue
		}
		quantity := max(1, evidence.ObservedQuantity)
		if valuation.PricingQuantity > 0 {
			quantity = valuation.PricingQuantity
		}
		if quantity > 1 && valuation.QuantityQuickSell <= 0 {
			continue
		}
		quickUnit := valuation.QuickSellValue
		if quantity > 1 {
			quickUnit = min64(quickUnit, valuation.QuantityQuickSell)
		}
		if quickUnit <= 0 || evidence.BestUnitRewardCents <= 0 {
			continue
		}
		completion := completionBPS(evidence, valuation)
		fresh := now.Sub(evidence.LastSeenAt) <= 30*time.Second
		ready := evidence.Tier == "actionable" && fresh && valuation.Volume24h >= 5 && valuation.PriceSellerCount >= 3
		state, reason := strings.ToUpper(evidence.Tier), evidence.Reason
		if ready {
			state, reason = "READY", ""
		}
		if evidence.Tier == "hold" {
			state = "HOLD"
		}
		if !fresh {
			state, reason = "STALE", "latest complete order evidence is older than 30 seconds"
		}
		if valuation.Volume24h < 5 || valuation.PriceSellerCount < 3 {
			if state == "READY" {
				state = "RESEARCH"
			}
			if reason == "" {
				reason = "auction exit lacks five near-target sales from three sellers"
			}
		}
		maxStack := max(1, evidence.MaxStackSize)
		inventorySlots := (quantity + maxStack - 1) / maxStack
		filledBatches := int(evidence.FilledUnits24h / int64(quantity))
		auctionBatches := valuation.Volume24h
		executable := min(filledBatches, auctionBatches)
		orderState, orderReason := state, reason
		if executable <= 0 {
			if orderState == "READY" {
				orderState = "RESEARCH"
			}
			if orderReason == "" {
				orderReason = "no conservative two-sided executable batch volume"
			}
		}

		competitiveUnitCents := evidence.BestUnitRewardCents + max64(1, evidence.BestUnitRewardCents/10_000)
		orderCost := centsForQuantity(competitiveUnitCents, int64(quantity), true)
		auctionGross := mulMoney(quickUnit, int64(quantity))
		auctionNet := applyFee(auctionGross, cfg.AuctionFeeBPS)
		cycle := max(1, valuation.ExpectedSellMinutes+estimatedFillMinutes(evidence, quantity))
		result = append(result, candidate(Candidate{
			ID: candidateID("order_to_auction", evidence.Signature, quantity), Route: "ORDER_TO_AUCTION", State: orderState, Reason: orderReason,
			Signature: evidence.Signature, ItemID: evidence.ItemID, ItemName: displayName(evidence), Quantity: quantity, MaxStackSize: maxStack,
			AcquisitionCost: orderCost, ExpectedProceeds: auctionNet, CompletionBPS: completion, ExpectedCycleMinutes: cycle,
			ExecutableBatches: executable, QueuePosition: max(1, evidence.BestPricePosition), OrderSlots: 1, AuctionSlots: 1, InventorySlots: inventorySlots,
			ConfidenceBPS: valuation.ConfidenceBPS, OrderTier: evidence.Tier, OrderFreshAt: evidence.LastSeenAt, AuctionFreshAt: valuation.GeneratedAt,
			OrderCommand: "/orders", AuctionCommand: auctionCommand(evidence.ItemID),
		}))

		activeUnit := valuation.ActiveBestAsk
		if activeUnit > 0 {
			auctionCost := mulMoney(activeUnit, int64(quantity))
			orderGross := centsForQuantity(evidence.BestUnitRewardCents, int64(quantity), false)
			orderNet := applyFee(orderGross, cfg.OrderFeeBPS)
			immediateExecutable := min(int(evidence.AvailableUnits/int64(quantity)), valuation.ActiveDepth)
			immediateState, immediateReason := state, reason
			if immediateExecutable <= 0 {
				if immediateState == "READY" {
					immediateState = "RESEARCH"
				}
				if immediateReason == "" {
					immediateReason = "no currently observed order capacity at this batch quantity"
				}
			}
			result = append(result, candidate(Candidate{
				ID: candidateID("auction_to_order", evidence.Signature, quantity), Route: "AUCTION_TO_ORDER", State: immediateState, Reason: immediateReason,
				Signature: evidence.Signature, ItemID: evidence.ItemID, ItemName: displayName(evidence), Quantity: quantity, MaxStackSize: maxStack,
				AcquisitionCost: auctionCost, ExpectedProceeds: orderNet, CompletionBPS: completion, ExpectedCycleMinutes: 2,
				ExecutableBatches: immediateExecutable, QueuePosition: max(1, evidence.BestPricePosition), OrderSlots: 0, AuctionSlots: 0, InventorySlots: inventorySlots,
				ConfidenceBPS: valuation.ConfidenceBPS, OrderTier: evidence.Tier, OrderFreshAt: evidence.LastSeenAt, AuctionFreshAt: valuation.GeneratedAt,
				OrderCommand: "/orders", AuctionCommand: auctionCommand(evidence.ItemID),
			}))
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].RiskAdjustedProfitDay != result[j].RiskAdjustedProfitDay {
			return result[i].RiskAdjustedProfitDay > result[j].RiskAdjustedProfitDay
		}
		if result[i].ProfitInventorySlot != result[j].ProfitInventorySlot {
			return result[i].ProfitInventorySlot > result[j].ProfitInventorySlot
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func candidate(value Candidate) Candidate {
	profit := value.ExpectedProceeds - value.AcquisitionCost
	value.ConservativeProfit = mulDivNonNegative(profit, int64(min(value.ConfidenceBPS, value.CompletionBPS)), 10_000)
	if profit > 0 && value.AcquisitionCost > 0 {
		ratio := float64(profit) / float64(value.AcquisitionCost) * 10_000
		value.MarginBPS = int(math.Min(float64(math.MaxInt32), ratio))
	}
	if value.InventorySlots > 0 {
		value.ProfitInventorySlot = value.ConservativeProfit / int64(value.InventorySlots)
	}
	if value.ExpectedCycleMinutes > 0 && value.ConservativeProfit > 0 {
		completed := mulDivNonNegative(value.ConservativeProfit, int64(value.CompletionBPS), 10_000)
		value.RiskAdjustedProfitDay = mulDivNonNegative(completed, 1440, int64(value.ExpectedCycleMinutes))
	}
	if profit <= 0 {
		value.State = "REJECTED"
		value.Reason = "conservative route is not profitable"
	}
	return value
}

func completionBPS(evidence Evidence, valuation market.Valuation) int {
	tier := 2_500
	if evidence.Tier == "research" {
		tier = 5_000
	}
	if evidence.Tier == "actionable" {
		tier = 8_000
	}
	if evidence.Tier == "hold" {
		tier = 1_000
	}
	queue := 4_000
	if evidence.BestPricePosition > 0 {
		queue = max(3_000, 10_000-(evidence.BestPricePosition-1)*500)
	}
	return min(min(tier, queue), valuation.ConfidenceBPS)
}

func estimatedFillMinutes(evidence Evidence, quantity int) int {
	if evidence.FilledUnits24h <= 0 {
		return 1440
	}
	minutes := int(int64(quantity) * 1440 / evidence.FilledUnits24h)
	return max(1, min(1440, minutes))
}

func displayName(evidence Evidence) string {
	if strings.TrimSpace(evidence.DisplayName) != "" {
		return evidence.DisplayName
	}
	name := strings.TrimPrefix(evidence.ItemID, "minecraft:")
	return strings.ReplaceAll(name, "_", " ")
}

func auctionCommand(itemID string) string {
	value := strings.TrimPrefix(strings.ToLower(itemID), "minecraft:")
	var safe strings.Builder
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_' || character == '-' {
			safe.WriteRune(character)
		}
	}
	if safe.Len() == 0 {
		return "/ah"
	}
	return "/ah " + safe.String()
}

func candidateID(route, signature string, quantity int) string {
	sum := sha256.Sum256([]byte(route + "\x00" + signature + "\x00" + fmtInt(quantity)))
	return route + "_" + hex.EncodeToString(sum[:12])
}

func fmtInt(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	buffer := [32]byte{}
	position := len(buffer)
	for value > 0 {
		position--
		buffer[position] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		position--
		buffer[position] = '-'
	}
	return string(buffer[position:])
}

func applyFee(value int64, bps int) int64 {
	return value - mulDivNonNegative(value, int64(bps), 10_000)
}
func mulMoney(left, right int64) int64 {
	if left <= 0 || right <= 0 {
		return 0
	}
	if left > math.MaxInt64/right {
		return math.MaxInt64
	}
	return left * right
}
func centsForQuantity(unitCents, quantity int64, roundUp bool) int64 {
	totalCents := mulMoney(unitCents, quantity)
	if totalCents == math.MaxInt64 {
		return math.MaxInt64
	}
	dollars := totalCents / 100
	if roundUp && totalCents%100 != 0 {
		dollars++
	}
	return dollars
}
func mulDivNonNegative(value, multiplier, divisor int64) int64 {
	if value <= 0 || multiplier <= 0 || divisor <= 0 {
		return 0
	}
	quotient, remainder := value/divisor, value%divisor
	if quotient > math.MaxInt64/multiplier {
		return math.MaxInt64
	}
	result := quotient * multiplier
	if remainder > math.MaxInt64/multiplier {
		return math.MaxInt64
	}
	extra := remainder * multiplier / divisor
	if result > math.MaxInt64-extra {
		return math.MaxInt64
	}
	return result + extra
}
func min64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}
