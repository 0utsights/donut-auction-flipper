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

const minimumExactExitConfidenceBPS = 3_500

type Config struct {
	DatabasePath   string
	AuctionFeeBPS  int
	OrderFeeBPS    int
	CandidateLimit int
}

type System struct {
	store       *Store
	cfg         Config
	now         func() time.Time
	version     atomic.Uint64
	lastRefresh atomic.Int64
	candidates  atomic.Pointer[CandidateFeed]
	refreshMu   sync.Mutex
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
func (s *System) ShouldYieldDiscovery(ctx context.Context, value Heartbeat) (bool, error) {
	return s.store.ShouldYieldDiscovery(ctx, value)
}
func (s *System) LeaseTask(ctx context.Context, observerID string) (*Task, error) {
	return s.store.LeaseTask(ctx, observerID, 30*time.Second)
}
func (s *System) SaveScan(ctx context.Context, value ScanBatch) (bool, error) {
	return s.store.SaveScan(ctx, value)
}
func (s *System) CompleteTask(ctx context.Context, value TaskResult) error {
	kind, err := s.store.LeasedTaskKind(ctx, value)
	if err != nil {
		return err
	}
	if err = s.store.CompleteTask(ctx, value); err != nil {
		return err
	}
	if kind != "discovery" || value.Status != "complete" {
		return nil
	}
	return s.queueAutomaticResearch(ctx)
}

func (s *System) queueAutomaticResearch(ctx context.Context) error {
	// The auction API establishes the exit value. READY markets take priority,
	// followed by RESEARCH markets. Within a tier, research the strongest
	// risk-adjusted opportunity first. Raw item value is only a tie-breaker;
	// otherwise a few expensive items can monopolize the observer indefinitely.
	feed := s.CandidateFeed()
	candidates := make([]Candidate, 0, len(feed.Candidates))
	for _, candidate := range feed.Candidates {
		needsOrderResearch := candidate.State == "READY" || (candidate.State == "RESEARCH" && candidate.OrderTier != "actionable")
		if candidate.Route == "ORDER_TO_AUCTION" && needsOrderResearch &&
			candidate.SignatureComplete && candidate.PriorityRank > 0 && candidate.PriorityScore > 0 &&
			candidate.TargetListPrice > 0 && candidate.ConservativeProfit > 0 {
			candidates = append(candidates, candidate)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].State != candidates[j].State {
			return candidates[i].State == "READY"
		}
		if candidates[i].PriorityScore != candidates[j].PriorityScore {
			return candidates[i].PriorityScore > candidates[j].PriorityScore
		}
		if candidates[i].ConservativeProfit != candidates[j].ConservativeProfit {
			return candidates[i].ConservativeProfit > candidates[j].ConservativeProfit
		}
		if candidates[i].TargetListPrice != candidates[j].TargetListPrice {
			return candidates[i].TargetListPrice > candidates[j].TargetListPrice
		}
		return candidates[i].Signature < candidates[j].Signature
	})
	signatures := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, exists := seen[candidate.Signature]; exists {
			continue
		}
		seen[candidate.Signature] = struct{}{}
		signatures = append(signatures, candidate.Signature)
	}
	return s.store.QueueAutomaticResearch(ctx, signatures, time.Minute, 5*time.Minute)
}
func (s *System) AddWatch(ctx context.Context, signature string) (Watch, error) {
	return s.store.AddWatch(ctx, signature, 15*time.Minute)
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
	return s.refreshLocked(ctx, engine)
}

// RefreshIfDue coalesces rapid collector pages and fast auction polls. It never
// queues behind another refresh, so the ingestion request path stays bounded.
func (s *System) RefreshIfDue(ctx context.Context, engine *market.Engine, interval time.Duration) (bool, error) {
	nowMillis := s.now().UnixMilli()
	last := s.lastRefresh.Load()
	if (last > 0 && nowMillis-last < interval.Milliseconds()) || !s.refreshMu.TryLock() {
		return false, nil
	}
	defer s.refreshMu.Unlock()
	last = s.lastRefresh.Load()
	if last > 0 && nowMillis-last < interval.Milliseconds() {
		return false, nil
	}
	if err := s.refreshLocked(ctx, engine); err != nil {
		return true, err
	}
	return true, nil
}

func (s *System) refreshLocked(ctx context.Context, engine *market.Engine) error {
	evidence, err := s.store.Evidence(ctx)
	if err != nil {
		return err
	}
	valuations := make(map[string]market.Valuation, len(evidence))
	for _, item := range evidence {
		// enrichEvidence deliberately clears historical prices that are absent
		// from the current one-hour research window. Those rows cannot produce a
		// candidate, so do not run the comparatively expensive quantity model
		// for hundreds of stale signatures on every live refresh.
		if item.BestUnitRewardCents <= 0 {
			continue
		}
		// Every stackable recommendation is a full-stack trade. The valuation must
		// therefore contain exact evidence for that same batch quantity. A singular
		// valuation remains a useful ceiling inside QuantityValuation, but can never
		// turn a 64-stack market into a misleading 1-item recommendation.
		quantity := max(1, item.MaxStackSize)
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
	s.lastRefresh.Store(s.now().UnixMilli())
	// Candidate economics may first become valid on the final submitted page,
	// after the discovery completion request has already used the preceding feed.
	// Queue here as well so a valuable API-backed market can preempt the next
	// discovery page instead of waiting for another complete traversal.
	return s.queueAutomaticResearch(ctx)
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
	selectedItems := map[string]struct{}{}
	result := []ReferenceSelection{}
	for _, value := range candidates {
		if value.Route != "ORDER_TO_AUCTION" || value.State != "READY" || value.AcquisitionCost <= 0 || value.RiskAdjustedProfitDay <= 0 {
			continue
		}
		if _, duplicate := selectedItems[value.ItemID]; duplicate {
			continue
		}
		maximum := value.ExecutableBatches
		maximum = min(maximum, int((deployable-cash)/value.AcquisitionCost))
		if value.OrderSlots > 0 && ordersUsed+value.OrderSlots > 20 {
			maximum = 0
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
		// One order can request multiple exact resale batches. It occupies one
		// order slot, while each concurrently listed batch consumes an auction slot.
		ordersUsed += value.OrderSlots
		auctionsUsed += value.AuctionSlots * maximum
		exactExposure[value.Signature] += capital
		itemExposure[value.ItemID] += capital
		selectedItems[value.ItemID] = struct{}{}
		result = append(result, ReferenceSelection{CandidateID: value.ID, ItemName: value.ItemName, Route: value.Route,
			Batches: maximum, OrderQuantity: safeIntProduct(value.Quantity, maximum), Capital: capital, RiskAdjustedProfitDay: mulMoney(value.RiskAdjustedProfitDay, int64(maximum))})
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
		// Orders are intentionally created in full-stack batches. Reject any
		// valuation that was built for a different quantity instead of silently
		// publishing a 1x recommendation for a stackable item.
		quantity := max(1, evidence.MaxStackSize)
		if valuation.PricingQuantity != quantity {
			continue
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
		orderUnitRewardCents := evidence.BestUnitRewardCents
		if evidence.FocusedUnitRewardCents > 0 && !evidence.FocusedSeenAt.IsZero() && !evidence.FocusedSeenAt.After(now) && now.Sub(evidence.FocusedSeenAt) <= 30*time.Second {
			orderUnitRewardCents = evidence.FocusedUnitRewardCents
		}
		completion := completionBPS(evidence, valuation)
		fresh := now.Sub(evidence.LastSeenAt) <= orderObservationWindow
		marketReason := marketHoldReason(valuation, now)
		ready := evidence.Tier == "actionable" && fresh && valuation.Volume24h >= 5 && valuation.PriceSellerCount >= 3 && valuation.ConfidenceBPS >= minimumExactExitConfidenceBPS && marketReason == ""
		state, reason := strings.ToUpper(evidence.Tier), evidence.Reason
		if ready {
			state, reason = "READY", ""
		}
		if evidence.Tier == "hold" {
			state = "HOLD"
		}
		if !fresh {
			state, reason = "STALE", "latest usable order observation is older than one hour"
		}
		if valuation.Volume24h < 5 || valuation.PriceSellerCount < 3 {
			if state == "READY" {
				state = "RESEARCH"
			}
			if reason == "" {
				reason = "auction exit lacks five near-target sales from three sellers"
			}
		}
		if valuation.ConfidenceBPS < minimumExactExitConfidenceBPS && reason == "" {
			state, reason = "RESEARCH", "exact-quantity auction confidence is below 35%"
		}
		if marketReason != "" {
			state, reason = "HOLD", marketReason
		}
		maxStack := max(1, evidence.MaxStackSize)
		inventorySlots := (quantity + maxStack - 1) / maxStack
		filledBatches := int(evidence.FilledUnits24h / int64(quantity))
		auctionBatches := valuation.Volume24h
		executable := min(filledBatches, auctionBatches)
		// Existing remaining order quantity is competing buyer demand, not proof
		// that sellers will fill our new order. Before measured fills qualify a
		// market, rank at most one exploratory batch. Confirmed fill velocity is
		// the only source of multi-batch ORDER_TO_AUCTION capacity.
		researchBatches := min(1, auctionBatches)
		orderState, orderReason := state, reason
		if executable <= 0 {
			if orderState == "READY" {
				orderState = "RESEARCH"
			}
			if orderReason == "" {
				orderReason = "no conservative two-sided executable batch volume"
			}
		}

		// Donut rewards are cent-precise. Beat the current best buy order by the
		// smallest representable amount instead of scaling the increment with price.
		competitiveUnitCents := orderUnitRewardCents + 1
		orderCost := centsForQuantity(competitiveUnitCents, int64(quantity), true)
		auctionGross := mulMoney(quickUnit, int64(quantity))
		auctionNet := applyFee(auctionGross, cfg.AuctionFeeBPS)
		cycle := max(1, valuation.ExpectedSellMinutes+estimatedFillMinutes(evidence, quantity))
		referenceAge := valuation.PriceReferenceAgeSeconds
		if referenceAge <= 0 {
			referenceAge = valuation.ReferenceAgeSeconds
		}
		result = append(result, candidate(Candidate{
			ID: candidateID("order_to_auction", evidence.Signature, quantity), Route: "ORDER_TO_AUCTION", State: orderState, Reason: orderReason,
			Signature: evidence.Signature, ItemID: evidence.ItemID, ItemName: displayName(evidence), Quantity: quantity, MaxStackSize: maxStack,
			AcquisitionCost: orderCost, ExpectedProceeds: auctionNet, OrderUnitRewardCents: competitiveUnitCents, TargetListPrice: auctionGross,
			CompletionBPS: completion, ExpectedCycleMinutes: cycle,
			ExecutableBatches: executable, ResearchBatches: researchBatches, QueuePosition: 1, OrderSlots: 1, AuctionSlots: 1, InventorySlots: inventorySlots,
			ConfidenceBPS: valuation.ConfidenceBPS, OrderTier: evidence.Tier, SignatureComplete: evidence.SignatureComplete, ResearchFreshAt: evidence.LastSeenAt, OrderFreshAt: evidence.FocusedSeenAt, FocusedFreshAt: evidence.FocusedSeenAt, AuctionFreshAt: valuation.GeneratedAt,
			AuctionVolume24h: valuation.Volume24h, AuctionSellerCount: valuation.PriceSellerCount, OrderFilledUnits24h: evidence.FilledUnits24h,
			OrderAvailableUnits: evidence.AvailableUnits, VolatilityBPS: valuation.VolatilityBPS, ReferenceAgeSeconds: referenceAge,
			RiskFlags:    append([]string(nil), valuation.RiskFlags...),
			OrderCommand: "/orders", AuctionCommand: auctionCommand(evidence.ItemID),
		}))

		activeUnit := valuation.ActiveBestAsk
		if activeUnit > 0 {
			auctionCost := mulMoney(activeUnit, int64(quantity))
			orderGross := centsForQuantity(orderUnitRewardCents, int64(quantity), false)
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
				AcquisitionCost: auctionCost, ExpectedProceeds: orderNet, OrderUnitRewardCents: orderUnitRewardCents,
				CompletionBPS: completion, ExpectedCycleMinutes: 2,
				ExecutableBatches: immediateExecutable, ResearchBatches: immediateExecutable, QueuePosition: 0, OrderSlots: 0, AuctionSlots: 0, InventorySlots: inventorySlots,
				ConfidenceBPS: valuation.ConfidenceBPS, OrderTier: evidence.Tier, SignatureComplete: evidence.SignatureComplete, ResearchFreshAt: evidence.LastSeenAt, OrderFreshAt: evidence.FocusedSeenAt, FocusedFreshAt: evidence.FocusedSeenAt, AuctionFreshAt: valuation.GeneratedAt,
				AuctionVolume24h: valuation.Volume24h, AuctionSellerCount: valuation.PriceSellerCount, OrderFilledUnits24h: evidence.FilledUnits24h,
				OrderAvailableUnits: evidence.AvailableUnits, VolatilityBPS: valuation.VolatilityBPS, ReferenceAgeSeconds: referenceAge,
				RiskFlags:    append([]string(nil), valuation.RiskFlags...),
				OrderCommand: "/orders", AuctionCommand: auctionCommand(evidence.ItemID),
			}))
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if candidateStateRank(result[i].State) != candidateStateRank(result[j].State) {
			return candidateStateRank(result[i].State) < candidateStateRank(result[j].State)
		}
		if result[i].PriorityScore != result[j].PriorityScore {
			return result[i].PriorityScore > result[j].PriorityScore
		}
		if result[i].ProfitInventorySlot != result[j].ProfitInventorySlot {
			return result[i].ProfitInventorySlot > result[j].ProfitInventorySlot
		}
		return result[i].ID < result[j].ID
	})
	rank := 0
	for index := range result {
		if result[index].SignatureComplete && (result[index].State == "READY" || result[index].State == "RESEARCH") && result[index].PriorityScore > 0 {
			rank++
			result[index].PriorityRank = rank
		}
	}
	return result
}

func candidate(value Candidate) Candidate {
	profit := value.ExpectedProceeds - value.AcquisitionCost
	value.GrossProfit = profit
	// Confidence discounts the exit value; completion probability is applied
	// separately below when converting that conservative profit into profit/day.
	value.ConservativeProfit = mulDivNonNegative(profit, int64(value.ConfidenceBPS), 10_000)
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
	capacity := value.ExecutableBatches
	if capacity <= 0 {
		capacity = value.ResearchBatches
	}
	if !value.SignatureComplete {
		capacity = 0
	}
	capacity = min(18, max(0, capacity))
	value.MaxOrderQuantity = safeIntProduct(value.Quantity, min(18, max(0, value.ExecutableBatches)))
	value.PriorityScore = mulMoney(value.RiskAdjustedProfitDay, int64(capacity))
	if profit <= 0 {
		value.State = "REJECTED"
		value.Reason = "conservative route is not profitable"
	}
	return value
}

func safeIntProduct(left, right int) int {
	if left <= 0 || right <= 0 {
		return 0
	}
	if left > math.MaxInt/right {
		return math.MaxInt
	}
	return left * right
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
	return min(tier, valuation.ConfidenceBPS)
}

func marketHoldReason(valuation market.Valuation, now time.Time) string {
	if valuation.GeneratedAt.IsZero() || now.Sub(valuation.GeneratedAt) > 2*time.Minute {
		return "auction valuation is older than two minutes"
	}
	priceAge := valuation.PriceReferenceAgeSeconds
	if priceAge <= 0 {
		priceAge = valuation.ReferenceAgeSeconds
	}
	// A fixed two-hour cutoff rejects most otherwise healthy five-sale/day
	// markets simply because their natural inter-sale interval is longer. Allow
	// two expected intervals, bounded tightly enough to reject genuinely stale
	// exits while keeping persistent commodity spreads visible.
	allowedAge := 48 * time.Hour / time.Duration(max(1, valuation.Volume24h))
	allowedAge = max(2*time.Hour, min(12*time.Hour, allowedAge))
	if priceAge > int64(allowedAge.Seconds()) {
		return "latest target-price auction sale is older than its volume-adjusted limit"
	}
	blocking := map[string]string{
		"api_modifier_blindspot":            "auction API evidence is modifier-blind",
		"falling_market":                    "auction exit market is falling",
		"high_volatility":                   "auction exit price is too volatile",
		"seller_concentration":              "auction evidence is concentrated in too few sellers",
		"stale_references":                  "auction sale references are stale",
		"target_price_seller_concentration": "target-price sales are seller-concentrated",
	}
	for _, flag := range valuation.RiskFlags {
		if reason := blocking[flag]; reason != "" {
			return reason
		}
	}
	return ""
}

func candidateStateRank(state string) int {
	switch state {
	case "READY":
		return 0
	case "RESEARCH":
		return 1
	case "HOLD":
		return 2
	case "STALE":
		return 3
	case "CAPTURED":
		return 4
	case "REJECTED":
		return 5
	default:
		return 6
	}
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
