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

const (
	minimumExactExitConfidenceBPS  = 3_500
	minimumFillerExitConfidenceBPS = 2_500
	minimumFillerAuctionVolume     = 2
	minimumFillerAuctionSellers    = 2
)

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
	research    atomic.Pointer[[]string]
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
	// The auction API establishes the exit value. Keep proven CORE markets warm,
	// then explore the API's price-first safe frontier before revisiting FILLER
	// rows. Otherwise a handful of already-profitable cheap rows can permanently
	// monopolize the only observer and prevent stronger items from being tested.
	feed := s.CandidateFeed()
	candidates := make([]Candidate, 0, len(feed.Candidates))
	for _, candidate := range feed.Candidates {
		needsOrderResearch := candidate.State == "READY" ||
			(candidate.Profiled && (candidate.State == "STALE" || candidate.State == "RESEARCH")) ||
			(candidate.State == "RESEARCH" && candidate.OrderTier != "actionable")
		if candidate.Route == "ORDER_TO_AUCTION" && needsOrderResearch &&
			candidate.SignatureComplete && candidate.PriorityScore > 0 &&
			candidate.TargetListPrice > 0 && candidate.ConservativeProfit > 0 {
			candidates = append(candidates, candidate)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		leftTier, rightTier := researchSchedulingTier(candidates[i]), researchSchedulingTier(candidates[j])
		if leftTier != rightTier {
			return leftTier < rightTier
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
	priorities := make(map[string]int, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	appendCandidate := func(candidate Candidate) {
		if _, exists := seen[candidate.Signature]; exists {
			return
		}
		seen[candidate.Signature] = struct{}{}
		signatures = append(signatures, candidate.Signature)
		if candidate.Profiled || (candidate.State == "READY" && candidate.OrderTier == "actionable") {
			priorities[candidate.Signature] = 75
		} else {
			priorities[candidate.Signature] = 50
		}
	}
	// A genuinely actionable or previously proven row deserves a quick current
	// recheck before exploration. Thin FILLER rows do not.
	for _, candidate := range candidates {
		if candidate.Profiled || (candidate.State == "READY" && candidate.OrderTier == "actionable") {
			appendCandidate(candidate)
		}
	}
	// Completed-auction history supplies a broad, safe shortlist even before an
	// item has current order evidence. Query these canonical items directly in
	// descending auction-value/volume order; once observed, normal candidate
	// economics replace this prior with the measured order spread.
	if targets := s.research.Load(); targets != nil {
		for _, signature := range *targets {
			if _, exists := seen[signature]; exists {
				continue
			}
			seen[signature] = struct{}{}
			signatures = append(signatures, signature)
			priorities[signature] = 50
		}
	}
	// Only after price-first API exploration do we spend observer time refreshing
	// replaceable FILLER rows and incomplete new candidates.
	for _, candidate := range candidates {
		appendCandidate(candidate)
	}
	// A profile may have no current candidate at all after its price sample ages
	// out. Keep rotating those known markets through the expedited read-only lane
	// so the system rebuilds current validity instead of relearning from scratch.
	profiled, err := s.store.ProfiledSignatures(ctx, 100)
	if err != nil {
		return err
	}
	for _, signature := range profiled {
		if _, exists := seen[signature]; exists {
			continue
		}
		seen[signature] = struct{}{}
		signatures = append(signatures, signature)
		priorities[signature] = 75
	}
	return s.store.QueueAutomaticResearch(ctx, signatures, priorities, 2*time.Second, 5*time.Minute, 20*time.Minute)
}

func researchSchedulingTier(candidate Candidate) int {
	if candidate.State == "READY" {
		return 0
	}
	if candidate.Profiled {
		return 1
	}
	return 2
}
func (s *System) AddWatch(ctx context.Context, signature string) (Watch, error) {
	return s.store.AddWatch(ctx, signature, time.Minute)
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
	now := s.now()
	marketSnapshot := engine.Snapshot()
	// Sixty direct-search targets provide enough breadth to find twenty positions
	// after unavailable and unprofitable spreads are discarded. CORE markets use
	// a separate faster recheck cadence, so this exploration cannot age them out.
	targets := auctionResearchTargets(marketSnapshot.Valuations, now, 60)
	s.research.Store(&targets)
	valuations := make(map[string]market.Valuation, len(evidence))
	for _, item := range evidence {
		if item.BestUnitRewardCents <= 0 {
			continue
		}
		// A listing can contain any exact quantity up to the item's stack limit.
		// Select the quantity with the strongest conservative per-listing profit
		// for which the API has exact-quantity evidence. Fabric may combine many of
		// these batches into one acquisition order and list them sequentially.
		valuation, ok := bestExitValuation(engine, item, s.cfg, now)
		if ok {
			valuations[item.Signature] = valuation
		}
	}
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

func bestExitValuation(engine *market.Engine, evidence Evidence, cfg Config, now time.Time) (market.Valuation, bool) {
	maximum := max(1, min(64, evidence.MaxStackSize))
	bestScore := int64(math.MinInt64)
	bestQuantity := 0
	var best market.Valuation
	valuations := engine.QuantityValuations(evidence.Signature, maximum)
	if len(valuations) == 0 && evidence.ItemID != evidence.Signature {
		valuations = engine.QuantityValuations(evidence.ItemID, maximum)
	}
	for _, valuation := range valuations {
		quantity := valuation.PricingQuantity
		if quantity < 1 || quantity > maximum || valuation.QuickSellValue <= 0 {
			continue
		}
		quickUnit := valuation.QuickSellValue
		if quantity > 1 {
			if valuation.QuantityQuickSell <= 0 {
				continue
			}
			quickUnit = min64(quickUnit, valuation.QuantityQuickSell)
		}
		gross := mulMoney(quickUnit, int64(quantity))
		net := applyFee(gross, cfg.AuctionFeeBPS)
		cost := centsForQuantity(effectiveCompetitiveOrderUnitReward(evidence, now), int64(quantity), true)
		profit := net - cost
		// Confidence is useful when comparing two profitable batch quantities. For
		// losing quantities retain the signed profit so the least-bad row remains
		// visible as REJECTED in engineering diagnostics.
		score := profit
		if profit > 0 {
			score = mulDivNonNegative(profit, int64(valuation.ConfidenceBPS), 10_000)
		}
		if score > bestScore || (score == bestScore && quantity > bestQuantity) {
			bestScore, bestQuantity, best = score, quantity, valuation
		}
	}
	return best, bestQuantity > 0
}

type rankedAuctionTarget struct {
	signature  string
	unitValue  int64
	volume     int
	confidence int
}

// Keep the API-first research frontier aligned with the collector's audited
// base-only signature policy. Variant-bearing markets can be observed safely,
// but cannot become actionable until component-aware identity exists, so they
// must not consume the sole observer's focused time.
var auctionResearchBaseItems = map[string]struct{}{
	"minecraft:ancient_debris": {}, "minecraft:amethyst_shard": {}, "minecraft:apple": {}, "minecraft:armadillo_scute": {},
	"minecraft:blaze_powder": {}, "minecraft:blaze_rod": {}, "minecraft:bone": {}, "minecraft:bone_meal": {}, "minecraft:blue_ice": {}, "minecraft:breeze_rod": {},
	"minecraft:charcoal": {}, "minecraft:coal": {}, "minecraft:coal_block": {}, "minecraft:cobblestone": {}, "minecraft:copper_ingot": {}, "minecraft:crying_obsidian": {},
	"minecraft:diamond": {}, "minecraft:diamond_block": {}, "minecraft:dirt": {}, "minecraft:emerald": {}, "minecraft:emerald_block": {}, "minecraft:end_crystal": {},
	"minecraft:ender_eye": {}, "minecraft:ender_pearl": {}, "minecraft:enchanted_golden_apple": {}, "minecraft:experience_bottle": {}, "minecraft:feather": {},
	"minecraft:fermented_spider_eye": {}, "minecraft:ghast_tear": {}, "minecraft:glass": {}, "minecraft:glow_ink_sac": {}, "minecraft:gold_ingot": {},
	"minecraft:gold_nugget": {}, "minecraft:golden_apple": {}, "minecraft:golden_carrot": {}, "minecraft:gravel": {}, "minecraft:gunpowder": {},
	"minecraft:heart_of_the_sea": {}, "minecraft:honey_block": {}, "minecraft:honeycomb": {}, "minecraft:honeycomb_block": {}, "minecraft:ink_sac": {},
	"minecraft:gilded_blackstone": {}, "minecraft:hopper": {}, "minecraft:iron_block": {}, "minecraft:iron_ingot": {}, "minecraft:iron_nugget": {},
	"minecraft:lapis_block": {}, "minecraft:lapis_lazuli": {}, "minecraft:leather": {}, "minecraft:magma_cream": {}, "minecraft:nether_quartz_ore": {},
	"minecraft:netherite_block": {}, "minecraft:netherite_ingot": {}, "minecraft:netherite_scrap": {}, "minecraft:obsidian": {}, "minecraft:phantom_membrane": {},
	"minecraft:dragon_head": {}, "minecraft:nether_star": {}, "minecraft:netherite_upgrade_smithing_template": {},
	"minecraft:prismarine_crystals": {}, "minecraft:prismarine_shard": {}, "minecraft:quartz": {}, "minecraft:quartz_block": {}, "minecraft:rabbit_foot": {},
	"minecraft:rabbit_hide": {}, "minecraft:raw_copper": {}, "minecraft:raw_copper_block": {}, "minecraft:raw_gold": {}, "minecraft:raw_gold_block": {},
	"minecraft:raw_iron": {}, "minecraft:raw_iron_block": {}, "minecraft:red_sand": {}, "minecraft:redstone": {}, "minecraft:redstone_block": {},
	"minecraft:rotten_flesh": {}, "minecraft:sand": {}, "minecraft:scute": {}, "minecraft:slime_ball": {}, "minecraft:spider_eye": {}, "minecraft:sponge": {},
	"minecraft:stone": {}, "minecraft:string": {}, "minecraft:totem_of_undying": {}, "minecraft:anvil": {}, "minecraft:blast_furnace": {},
	"minecraft:bone_block": {}, "minecraft:bookshelf": {}, "minecraft:carved_pumpkin": {}, "minecraft:cauldron": {}, "minecraft:chipped_anvil": {},
	"minecraft:cobweb": {}, "minecraft:dead_fire_coral_fan": {}, "minecraft:diamond_ore": {}, "minecraft:fletching_table": {}, "minecraft:glass_bottle": {},
	"minecraft:glowstone": {}, "minecraft:glowstone_dust": {}, "minecraft:ice": {}, "minecraft:jukebox": {}, "minecraft:lever": {}, "minecraft:note_block": {},
	"minecraft:oxidized_copper_bulb": {}, "minecraft:pale_oak_shelf": {}, "minecraft:polished_blackstone": {}, "minecraft:quartz_stairs": {}, "minecraft:rail": {},
	"minecraft:redstone_lamp": {}, "minecraft:redstone_torch": {}, "minecraft:sculk_catalyst": {}, "minecraft:sea_lantern": {}, "minecraft:slime_block": {},
	"minecraft:sticky_piston": {}, "minecraft:stripped_acacia_log": {}, "minecraft:target": {}, "minecraft:tinted_glass": {}, "minecraft:warped_trapdoor": {},
	"minecraft:waxed_oxidized_copper_bulb": {}, "minecraft:wind_charge": {}, "minecraft:white_wool": {}, "minecraft:red_wool": {}, "minecraft:gray_wool": {},
	"minecraft:lime_concrete": {}, "minecraft:yellow_concrete": {}, "minecraft:black_glazed_terracotta": {}, "minecraft:green_glazed_terracotta": {},
}

func auctionResearchTargets(values map[string]market.Valuation, now time.Time, limit int) []string {
	best := make(map[string]rankedAuctionTarget)
	for _, value := range values {
		signature := value.BaseSignature
		if signature == "" {
			signature = value.Signature
		}
		if !canonicalBaseItem(signature) || value.QuickSellValue <= 0 || value.Volume24h < minimumFillerAuctionVolume ||
			value.PriceSellerCount < minimumFillerAuctionSellers || value.ConfidenceBPS < minimumFillerExitConfidenceBPS || marketHoldReason(value, now, true) != "" {
			continue
		}
		target := rankedAuctionTarget{signature: signature, unitValue: value.QuickSellValue, volume: value.Volume24h, confidence: value.ConfidenceBPS}
		if current, exists := best[signature]; !exists || auctionTargetLess(current, target) {
			best[signature] = target
		}
	}
	ranked := make([]rankedAuctionTarget, 0, len(best))
	for _, target := range best {
		ranked = append(ranked, target)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		return auctionTargetLess(ranked[j], ranked[i])
	})
	limit = min(max(0, limit), len(ranked))
	result := make([]string, 0, limit)
	seen := make(map[string]struct{}, limit)
	// Reserve two thirds of the bounded frontier for reliable high unit values.
	// The remaining third is an economic-throughput lane so cheaper, genuinely
	// high-volume markets still get tested instead of being excluded forever.
	priceSlots := min(limit, max(1, limit*2/3))
	for _, target := range ranked[:priceSlots] {
		result = append(result, target.signature)
		seen[target.signature] = struct{}{}
	}
	throughput := append([]rankedAuctionTarget(nil), ranked...)
	sort.SliceStable(throughput, func(i, j int) bool {
		left := mulMoney(throughput[i].unitValue, int64(min(throughput[i].volume, 20)))
		right := mulMoney(throughput[j].unitValue, int64(min(throughput[j].volume, 20)))
		if left != right {
			return left > right
		}
		return auctionTargetLess(throughput[j], throughput[i])
	})
	for _, target := range throughput {
		if len(result) >= limit {
			break
		}
		if _, exists := seen[target.signature]; exists {
			continue
		}
		seen[target.signature] = struct{}{}
		result = append(result, target.signature)
	}
	// Small test/development feeds can exhaust the throughput lane with rows
	// already selected above; preserve the remaining price ordering in that case.
	for _, target := range ranked {
		if len(result) >= limit {
			break
		}
		if _, exists := seen[target.signature]; exists {
			continue
		}
		seen[target.signature] = struct{}{}
		result = append(result, target.signature)
	}
	return result
}

func auctionTargetLess(left, right rankedAuctionTarget) bool {
	if left.unitValue != right.unitValue {
		return left.unitValue < right.unitValue
	}
	if left.volume != right.volume {
		return left.volume < right.volume
	}
	if left.confidence != right.confidence {
		return left.confidence < right.confidence
	}
	return left.signature > right.signature
}

func canonicalBaseItem(signature string) bool {
	const prefix = "minecraft:"
	if !strings.HasPrefix(signature, prefix) {
		return false
	}
	path := strings.TrimPrefix(signature, prefix)
	if path == "" || len(path) > 64 {
		return false
	}
	for _, character := range path {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	switch path {
	case "buy", "cancel", "claim", "collect", "confirm", "create", "fulfill", "help", "list", "my", "purchase", "reload", "search", "sell":
		return false
	}
	_, allowed := auctionResearchBaseItems[signature]
	return allowed
}

func (s *System) Debug(ctx context.Context) (DebugSnapshot, error) {
	debug, err := s.store.Debug(ctx)
	if err != nil {
		return DebugSnapshot{}, err
	}
	debug.Candidates = s.CandidateFeed().Candidates
	return debug, nil
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
		// Exact resale quantity is chosen from API-supported quantities between 1
		// and the stack cap. This is not necessarily a full stack: expensive items
		// can use 1x exits while cheaper commodities use a proven 64x exit.
		quantity := valuation.PricingQuantity
		if quantity < 1 || quantity > max(1, min(64, evidence.MaxStackSize)) {
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
		orderUnitRewardCents := effectiveOrderUnitReward(evidence, now)
		competitiveUnitCents := effectiveCompetitiveOrderUnitReward(evidence, now)
		completion := completionBPS(evidence, valuation)
		fresh := now.Sub(evidence.LastSeenAt) <= orderObservationWindow
		marketReason := marketHoldReason(valuation, now, false)
		fillerMarketReason := marketHoldReason(valuation, now, true)
		ready := evidence.Tier == "actionable" && evidence.FilledUnits24h >= int64(quantity) && fresh && valuation.Volume24h >= 5 && valuation.PriceSellerCount >= 3 && valuation.ConfidenceBPS >= minimumExactExitConfidenceBPS && marketReason == ""
		// A cancelable order slot has option value even when the market has not
		// yet proven enough volume for a large position. Admit one exploratory
		// exact-stack filler when the current order price is stable, the item is
		// modifier-safe, and two independent near-target auction exits support a
		// conservative profit. Fabric still performs its six-second final recheck.
		fillerReady := !ready && (evidence.Tier == "research" || evidence.Tier == "actionable") &&
			evidence.CompleteScans >= 3 && evidence.Stable && evidence.SignatureComplete && fresh &&
			valuation.Volume24h >= minimumFillerAuctionVolume && valuation.PriceSellerCount >= minimumFillerAuctionSellers &&
			valuation.ConfidenceBPS >= minimumFillerExitConfidenceBPS && fillerMarketReason == ""
		state, reason := strings.ToUpper(evidence.Tier), evidence.Reason
		// Evidence maturity and candidate executability are different state
		// machines. An actionable order profile still remains RESEARCH until its
		// exact auction exit passes the current liquidity and risk gates.
		if state == "ACTIONABLE" {
			state = "RESEARCH"
		}
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
		if fillerReady {
			orderState = "READY"
			orderReason = "filler: profitable exact-stack exit with thinner measured volume; start with one stack and replace it when a stronger market qualifies"
			executable = 1
		}
		if executable <= 0 {
			if orderState == "READY" {
				orderState = "RESEARCH"
			}
			if orderReason == "" {
				orderReason = "no conservative two-sided executable batch volume"
			}
		}

		// The menu abbreviates high prices, so the collector supplies the next
		// visible bucket boundary. This is the lowest bid guaranteed to exceed any
		// hidden value that could render as the observed token if Donut truncates.
		// Candidate economics are computed from this actual proposed bid.
		orderCost := centsForQuantity(competitiveUnitCents, int64(quantity), true)
		auctionGross := mulMoney(quickUnit, int64(quantity))
		auctionNet := applyFee(auctionGross, cfg.AuctionFeeBPS)
		listGross := recommendedListGross(valuation, quantity)
		cycle := max(1, valuation.ExpectedSellMinutes+estimatedFillMinutes(evidence, quantity))
		referenceAge := valuation.PriceReferenceAgeSeconds
		if referenceAge <= 0 {
			referenceAge = valuation.ReferenceAgeSeconds
		}
		result = append(result, candidate(Candidate{
			ID: candidateID("order_to_auction", evidence.Signature, quantity), Route: "ORDER_TO_AUCTION", State: orderState, Reason: orderReason,
			Signature: evidence.Signature, ItemID: evidence.ItemID, ItemName: displayName(evidence), Quantity: quantity, MaxStackSize: maxStack,
			AcquisitionCost: orderCost, ExpectedProceeds: auctionNet, ObservedOrderUnitRewardCents: orderUnitRewardCents, OrderUnitRewardCents: competitiveUnitCents, TargetListPrice: listGross,
			CompletionBPS: completion, ExpectedCycleMinutes: cycle,
			ExecutableBatches: executable, ResearchBatches: researchBatches, QueuePosition: 1, OrderSlots: 1, AuctionSlots: 1, InventorySlots: inventorySlots,
			ConfidenceBPS: valuation.ConfidenceBPS, OrderTier: evidence.Tier, SignatureComplete: evidence.SignatureComplete, ResearchFreshAt: evidence.LastSeenAt, OrderFreshAt: evidence.FocusedSeenAt, FocusedFreshAt: evidence.FocusedSeenAt, AuctionFreshAt: valuation.GeneratedAt,
			AuctionVolume24h: valuation.Volume24h, AuctionSellerCount: valuation.PriceSellerCount, OrderFilledUnits24h: evidence.FilledUnits24h,
			OrderAvailableUnits: evidence.AvailableUnits, Profiled: evidence.Profiled, ProfileFillEvents: evidence.ProfileFillEvents,
			ProfileDistinctOrders: evidence.ProfileDistinctOrders, VolatilityBPS: valuation.VolatilityBPS, ReferenceAgeSeconds: referenceAge,
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
				AcquisitionCost: auctionCost, ExpectedProceeds: orderNet, ObservedOrderUnitRewardCents: orderUnitRewardCents, OrderUnitRewardCents: orderUnitRewardCents,
				CompletionBPS: completion, ExpectedCycleMinutes: 2,
				ExecutableBatches: immediateExecutable, ResearchBatches: immediateExecutable, QueuePosition: 0, OrderSlots: 0, AuctionSlots: 0, InventorySlots: inventorySlots,
				ConfidenceBPS: valuation.ConfidenceBPS, OrderTier: evidence.Tier, SignatureComplete: evidence.SignatureComplete, ResearchFreshAt: evidence.LastSeenAt, OrderFreshAt: evidence.FocusedSeenAt, FocusedFreshAt: evidence.FocusedSeenAt, AuctionFreshAt: valuation.GeneratedAt,
				AuctionVolume24h: valuation.Volume24h, AuctionSellerCount: valuation.PriceSellerCount, OrderFilledUnits24h: evidence.FilledUnits24h,
				OrderAvailableUnits: evidence.AvailableUnits, Profiled: evidence.Profiled, ProfileFillEvents: evidence.ProfileFillEvents,
				ProfileDistinctOrders: evidence.ProfileDistinctOrders, VolatilityBPS: valuation.VolatilityBPS, ReferenceAgeSeconds: referenceAge,
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

// recommendedListGross is the player-facing listing price. Candidate profit is
// deliberately calculated from QuickSellValue instead, so displaying the
// observed clearing price cannot make the risk model optimistic. When current
// competition is below that clearing price, undercut the second independent
// seller by $1,000 for the whole listing; ActiveReferenceAsk intentionally
// resists one bait listing.
func recommendedListGross(valuation market.Valuation, quantity int) int64 {
	quickGross := mulMoney(valuation.QuickSellValue, int64(quantity))
	unitTarget := valuation.FairValue
	if unitTarget <= 0 {
		unitTarget = valuation.QuickSellValue
	}
	target := mulMoney(unitTarget, int64(quantity))
	if valuation.ActiveReferenceAsk > 0 {
		competitive := mulMoney(valuation.ActiveReferenceAsk, int64(quantity))
		if competitive > 1_000 {
			competitive -= 1_000
		}
		if competitive > 0 {
			target = min64(target, competitive)
		}
	}
	return max64(quickGross, target)
}

func effectiveOrderUnitReward(evidence Evidence, now time.Time) int64 {
	if evidence.FocusedUnitRewardCents > 0 && !evidence.FocusedSeenAt.IsZero() && !evidence.FocusedSeenAt.After(now) && now.Sub(evidence.FocusedSeenAt) <= 30*time.Second {
		return evidence.FocusedUnitRewardCents
	}
	return evidence.BestUnitRewardCents
}

func effectiveCompetitiveOrderUnitReward(evidence Evidence, now time.Time) int64 {
	focused := evidence.FocusedUnitRewardCents > 0 && !evidence.FocusedSeenAt.IsZero() &&
		!evidence.FocusedSeenAt.After(now) && now.Sub(evidence.FocusedSeenAt) <= 30*time.Second
	if focused {
		if evidence.FocusedCompetitiveUnitRewardCents > evidence.FocusedUnitRewardCents {
			return evidence.FocusedCompetitiveUnitRewardCents
		}
		return evidence.FocusedUnitRewardCents + 1
	}
	if evidence.BestCompetitiveUnitRewardCents > evidence.BestUnitRewardCents {
		return evidence.BestCompetitiveUnitRewardCents
	}
	// This compatibility path is only reachable by directly constructed test
	// evidence. Stored observations without a collector-supplied bucket bound are
	// excluded by Store.enrichEvidence and cannot produce live candidates.
	return evidence.BestUnitRewardCents + 1
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
	capacity = max(0, capacity)
	// The backend publishes market capacity, not a balance-sized recommendation.
	// Acquisition orders may contain many exact resale batches; Fabric alone
	// chooses an affordable quantity. Each auction listing is still one exact
	// batch (normally at most 64), and those listings can reuse slots over time.
	value.MaxOrderQuantity = safeIntProduct(value.Quantity, max(0, value.ExecutableBatches))
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

func marketHoldReason(valuation market.Valuation, now time.Time, allowThinSellerEvidence bool) string {
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
		if allowThinSellerEvidence && valuation.Volume24h >= minimumFillerAuctionVolume &&
			valuation.PriceSellerCount >= minimumFillerAuctionSellers &&
			(flag == "seller_concentration" || flag == "target_price_seller_concentration") {
			continue
		}
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
