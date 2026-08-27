package orders

import "time"

const SchemaVersion = "orders-v1"

type ObserverRegistration struct {
	ObserverID    string   `json:"observer_id"`
	ParserVersion string   `json:"parser_version"`
	ProxyLabel    string   `json:"proxy_label"`
	Capabilities  []string `json:"capabilities,omitempty"`
}

type Observer struct {
	ObserverID     string    `json:"observer_id"`
	ParserVersion  string    `json:"parser_version"`
	ProxyLabel     string    `json:"proxy_label"`
	State          string    `json:"state"`
	CurrentTaskID  string    `json:"current_task_id,omitempty"`
	CurrentPage    int       `json:"current_page,omitempty"`
	LatencyMS      float64   `json:"latency_ms,omitempty"`
	ReconnectCount int       `json:"reconnect_count"`
	LastSeenAt     time.Time `json:"last_seen_at"`
}

type Heartbeat struct {
	ObserverID     string  `json:"observer_id"`
	State          string  `json:"state"`
	TaskID         string  `json:"task_id,omitempty"`
	LeaseToken     string  `json:"lease_token,omitempty"`
	Page           int     `json:"page,omitempty"`
	LatencyMS      float64 `json:"latency_ms,omitempty"`
	ReconnectCount int     `json:"reconnect_count,omitempty"`
}

type Task struct {
	ID               string    `json:"id"`
	Kind             string    `json:"kind"`
	Signature        string    `json:"signature,omitempty"`
	Priority         int       `json:"priority"`
	DesiredFreshness int       `json:"desired_freshness_ms"`
	ParserSchema     string    `json:"parser_schema"`
	LeaseExpiresAt   time.Time `json:"lease_expires_at"`
	LeaseToken       string    `json:"lease_token"`
}

type OrderObservation struct {
	OrderKey          string    `json:"order_key"`
	ItemID            string    `json:"item_id"`
	Signature         string    `json:"signature"`
	DisplayName       string    `json:"display_name,omitempty"`
	Quantity          int       `json:"quantity"`
	MaxStackSize      int       `json:"max_stack_size"`
	UnitRewardCents   int64     `json:"unit_reward_cents"`
	RequestedQuantity int64     `json:"requested_quantity"`
	RemainingQuantity int64     `json:"remaining_quantity"`
	Owner             string    `json:"owner,omitempty"`
	ExpiresAt         time.Time `json:"expires_at,omitempty"`
	PricePosition     int       `json:"price_position,omitempty"`
	Slot              int       `json:"slot"`
	RawFieldHash      string    `json:"raw_field_hash"`
	SignatureComplete bool      `json:"signature_complete"`
	IdentityVerified  bool      `json:"identity_verified"`
}

type ScanBatch struct {
	SchemaVersion string             `json:"schema_version"`
	ObserverID    string             `json:"observer_id"`
	TaskID        string             `json:"task_id,omitempty"`
	LeaseToken    string             `json:"lease_token,omitempty"`
	SessionID     string             `json:"session_id"`
	ContentHash   string             `json:"content_hash"`
	ScreenTitle   string             `json:"screen_title"`
	Page          int                `json:"page"`
	Complete      bool               `json:"complete"`
	ObservedAt    time.Time          `json:"observed_at"`
	Orders        []OrderObservation `json:"orders"`
	UnknownSchema bool               `json:"unknown_schema,omitempty"`
	SchemaReason  string             `json:"schema_reason,omitempty"`
}

type TaskResult struct {
	ObserverID string `json:"observer_id"`
	TaskID     string `json:"task_id"`
	LeaseToken string `json:"lease_token"`
	Status     string `json:"status"`
	Message    string `json:"message,omitempty"`
}

type WatchRequest struct {
	Signature string `json:"signature"`
}

type Watch struct {
	ID        string    `json:"id"`
	Signature string    `json:"signature"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Diagnostic struct {
	InstallID string            `json:"install_id"`
	Version   string            `json:"version"`
	Event     string            `json:"event"`
	Code      string            `json:"code,omitempty"`
	Duration  int64             `json:"duration_ms,omitempty"`
	Fields    map[string]string `json:"fields,omitempty"`
	CreatedAt time.Time         `json:"created_at,omitempty"`
}

type Evidence struct {
	Signature              string    `json:"signature"`
	ItemID                 string    `json:"item_id"`
	DisplayName            string    `json:"display_name"`
	Tier                   string    `json:"tier"`
	CompleteScans          int       `json:"complete_scans"`
	FillEvents             int       `json:"fill_events"`
	DistinctOrders         int       `json:"distinct_orders"`
	FilledUnits24h         int64     `json:"filled_units_24h"`
	ProfileFillEvents      int       `json:"profile_fill_events_90d"`
	ProfileDistinctOrders  int       `json:"profile_distinct_orders_90d"`
	Profiled               bool      `json:"profiled"`
	ProfileLastFillAt      time.Time `json:"profile_last_fill_at,omitempty"`
	AvailableUnits         int64     `json:"available_units"`
	BestUnitRewardCents    int64     `json:"best_unit_reward_cents"`
	BestPricePosition      int       `json:"best_price_position"`
	ObservedQuantity       int       `json:"observed_quantity"`
	MaxStackSize           int       `json:"max_stack_size"`
	FirstSeenAt            time.Time `json:"first_seen_at"`
	LastSeenAt             time.Time `json:"last_seen_at"`
	FocusedSeenAt          time.Time `json:"focused_seen_at"`
	FocusedUnitRewardCents int64     `json:"focused_unit_reward_cents"`
	Stable                 bool      `json:"stable"`
	Conflict               bool      `json:"conflict"`
	SignatureComplete      bool      `json:"signature_complete"`
	Reason                 string    `json:"reason,omitempty"`
}

type Candidate struct {
	ID                    string    `json:"id"`
	PriorityRank          int       `json:"priority_rank"`
	PriorityScore         int64     `json:"priority_score"`
	Route                 string    `json:"route"`
	State                 string    `json:"state"`
	Reason                string    `json:"reason,omitempty"`
	Signature             string    `json:"signature"`
	ItemID                string    `json:"item_id"`
	ItemName              string    `json:"item_name"`
	Quantity              int       `json:"quantity"`
	MaxStackSize          int       `json:"max_stack_size"`
	AcquisitionCost       int64     `json:"acquisition_cost"`
	ExpectedProceeds      int64     `json:"expected_proceeds"`
	OrderUnitRewardCents  int64     `json:"order_unit_reward_cents"`
	TargetListPrice       int64     `json:"target_list_price"`
	GrossProfit           int64     `json:"gross_profit"`
	ConservativeProfit    int64     `json:"conservative_profit"`
	MarginBPS             int       `json:"margin_bps"`
	CompletionBPS         int       `json:"completion_bps"`
	ExpectedCycleMinutes  int       `json:"expected_cycle_minutes"`
	RiskAdjustedProfitDay int64     `json:"risk_adjusted_profit_day"`
	ExecutableBatches     int       `json:"executable_batches"`
	MaxOrderQuantity      int       `json:"max_order_quantity"`
	ResearchBatches       int       `json:"research_batches"`
	QueuePosition         int       `json:"queue_position"`
	OrderSlots            int       `json:"order_slots"`
	AuctionSlots          int       `json:"auction_slots"`
	InventorySlots        int       `json:"inventory_slots"`
	ProfitInventorySlot   int64     `json:"profit_per_inventory_slot"`
	ConfidenceBPS         int       `json:"confidence_bps"`
	AuctionVolume24h      int       `json:"auction_volume_24h"`
	AuctionSellerCount    int       `json:"auction_seller_count"`
	OrderFilledUnits24h   int64     `json:"order_filled_units_24h"`
	OrderAvailableUnits   int64     `json:"order_available_units"`
	Profiled              bool      `json:"profiled"`
	ProfileFillEvents     int       `json:"profile_fill_events_90d"`
	ProfileDistinctOrders int       `json:"profile_distinct_orders_90d"`
	VolatilityBPS         int       `json:"volatility_bps"`
	ReferenceAgeSeconds   int64     `json:"reference_age_seconds"`
	RiskFlags             []string  `json:"risk_flags,omitempty"`
	OrderTier             string    `json:"order_tier"`
	SignatureComplete     bool      `json:"signature_complete"`
	ResearchFreshAt       time.Time `json:"research_fresh_at"`
	OrderFreshAt          time.Time `json:"order_fresh_at"`
	FocusedFreshAt        time.Time `json:"focused_fresh_at"`
	AuctionFreshAt        time.Time `json:"auction_fresh_at"`
	OrderCommand          string    `json:"order_command"`
	AuctionCommand        string    `json:"auction_command"`
}

type CandidateFeed struct {
	Version     uint64      `json:"version"`
	GeneratedAt time.Time   `json:"generated_at"`
	Candidates  []Candidate `json:"candidates"`
}

// ReferenceMinimumProfitPerExit mirrors Fabric's balance-scaled slot floor for
// the public $10M reference view. Player-specific decisions remain local.
func ReferenceMinimumProfitPerExit(candidates []Candidate, balance int64, freeAuctionSlots int) int64 {
	qualityTotal, qualityCount := 0, 0
	for _, candidate := range candidates {
		if candidate.Route != "ORDER_TO_AUCTION" || candidate.State != "READY" || candidate.ConservativeProfit <= 0 {
			continue
		}
		qualityTotal += min(candidate.ConfidenceBPS, candidate.CompletionBPS)
		qualityCount++
	}
	quality := 0
	if qualityCount > 0 {
		quality = qualityTotal / qualityCount
	}
	reserveBPS := max(1_500, min(3_500, 3_500-quality*2_000/10_000))
	deployable := mulDivNonNegative(max64(0, balance), int64(10_000-reserveBPS), 10_000)
	return mulDivNonNegative(deployable/int64(max(1, freeAuctionSlots)), 100, 10_000)
}

type DebugSnapshot struct {
	Observers          []Observer           `json:"observers"`
	Evidence           []Evidence           `json:"evidence"`
	Watches            []Watch              `json:"watches"`
	Candidates         []Candidate          `json:"candidates"`
	ScanCoverage       ScanCoverage         `json:"scan_coverage"`
	RecentFills        []FillEvidence       `json:"recent_fills"`
	ReferencePortfolio []ReferenceSelection `json:"reference_portfolio"`
	Diagnostics        int                  `json:"diagnostics_14d"`
	GeneratedAt        time.Time            `json:"generated_at"`
}

type ReferenceSelection struct {
	CandidateID           string `json:"candidate_id"`
	ItemName              string `json:"item_name"`
	Route                 string `json:"route"`
	Batches               int    `json:"batches"`
	OrderQuantity         int    `json:"order_quantity"`
	Capital               int64  `json:"capital"`
	RiskAdjustedProfitDay int64  `json:"risk_adjusted_profit_day"`
}

type ScanCoverage struct {
	Total              int       `json:"total"`
	Complete           int       `json:"complete"`
	Incomplete         int       `json:"incomplete"`
	UnknownSchema      int       `json:"unknown_schema"`
	DistinctPages      int       `json:"distinct_pages"`
	HighestPage        int       `json:"highest_page"`
	ConfirmedFills     int       `json:"confirmed_fills"`
	QuarantinedFills   int       `json:"quarantined_fills"`
	CompleteSignatures int       `json:"complete_signatures"`
	LastScanAt         time.Time `json:"last_scan_at"`
}

type FillEvidence struct {
	Signature          string    `json:"signature"`
	OrderKey           string    `json:"order_key"`
	ObserverID         string    `json:"observer_id"`
	Units              int64     `json:"units"`
	UnitRewardCents    int64     `json:"unit_reward_cents"`
	ConfirmationLevel  int       `json:"confirmation_level"`
	PreviousObservedAt time.Time `json:"previous_observed_at"`
	ObservedAt         time.Time `json:"observed_at"`
}
