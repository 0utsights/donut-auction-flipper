package market

import "time"

type Source string

const (
	SourceDonutAPI Source = "donut_api"
)

type Item struct {
	ID            string            `json:"id"`
	Quantity      int               `json:"quantity"`
	DisplayName   string            `json:"display_name,omitempty"`
	Lore          []string          `json:"lore,omitempty"`
	Enchantments  map[string]int    `json:"enchantments,omitempty"`
	TrimPattern   string            `json:"trim_pattern,omitempty"`
	TrimMaterial  string            `json:"trim_material,omitempty"`
	Durability    int               `json:"durability,omitempty"`
	MaxDurability int               `json:"max_durability,omitempty"`
	Components    map[string]string `json:"components,omitempty"`
	Contents      []Item            `json:"contents,omitempty"`
}

type Signature struct {
	Exact     string `json:"exact"`
	Base      string `json:"base"`
	Modifiers string `json:"modifiers,omitempty"`
}

type Listing struct {
	Fingerprint     string    `json:"fingerprint"`
	AuthoritativeID string    `json:"authoritative_id,omitempty"`
	SellerUUID      string    `json:"seller_uuid,omitempty"`
	SellerName      string    `json:"seller_name"`
	Item            Item      `json:"item"`
	Signature       Signature `json:"signature"`
	TotalPrice      int64     `json:"total_price"`
	UnitPrice       int64     `json:"unit_price"`
	FirstSeen       time.Time `json:"first_seen"`
	LastSeen        time.Time `json:"last_seen"`
	ExpiresAt       time.Time `json:"expires_at,omitempty"`
	Source          Source    `json:"source"`
	SearchContext   string    `json:"search_context,omitempty"`
	Page            int       `json:"page,omitempty"`
	ObserverCount   int       `json:"observer_count"`
}

type Transaction struct {
	Fingerprint string    `json:"fingerprint"`
	SellerUUID  string    `json:"seller_uuid,omitempty"`
	SellerName  string    `json:"seller_name"`
	Item        Item      `json:"item"`
	Signature   Signature `json:"signature"`
	TotalPrice  int64     `json:"total_price"`
	UnitPrice   int64     `json:"unit_price"`
	SoldAt      time.Time `json:"sold_at"`
	Source      Source    `json:"source"`
}

type Valuation struct {
	Signature                string    `json:"signature"`
	BaseSignature            string    `json:"base_signature"`
	FairValue                int64     `json:"fair_value"`
	QuickSellValue           int64     `json:"quick_sell_value"`
	SingularQuickSell        int64     `json:"singular_quick_sell,omitempty"`
	QuantityQuickSell        int64     `json:"quantity_quick_sell,omitempty"`
	PricingQuantity          int       `json:"pricing_quantity,omitempty"`
	SingularVolume24h        int       `json:"singular_volume_24h,omitempty"`
	QuantityVolume24h        int       `json:"quantity_volume_24h,omitempty"`
	ShortTermValue           int64     `json:"short_term_value"`
	LongTermValue            int64     `json:"long_term_value"`
	ActiveBestAsk            int64     `json:"active_best_ask"`
	ActiveReferenceAsk       int64     `json:"active_reference_ask"`
	ActiveDepth              int       `json:"active_depth"`
	ActiveSellerCount        int       `json:"active_seller_count"`
	ConfidenceBPS            int       `json:"confidence_bps"`
	Volume24h                int       `json:"volume_24h"`
	MarketVolume24h          int       `json:"market_volume_24h"`
	PriceSellerCount         int       `json:"price_seller_count"`
	PriceBandLow             int64     `json:"price_band_low"`
	PriceBandHigh            int64     `json:"price_band_high"`
	SampleCount              int       `json:"sample_count"`
	RawSampleCount           int       `json:"raw_sample_count"`
	SellerCount              int       `json:"seller_count"`
	FreshSampleCount         int       `json:"fresh_sample_count"`
	VolatilityBPS            int       `json:"volatility_bps"`
	SpreadBPS                int       `json:"spread_bps"`
	ExpectedSellMinutes      int       `json:"expected_sell_minutes"`
	ReferenceAgeSeconds      int64     `json:"reference_age_seconds"`
	PriceReferenceAgeSeconds int64     `json:"price_reference_age_seconds"`
	ShortWindowHours         int       `json:"short_window_hours"`
	Regime                   string    `json:"regime"`
	RiskFlags                []string  `json:"risk_flags,omitempty"`
	ModelVersion             string    `json:"model_version"`
	FallbackLevel            string    `json:"fallback_level"`
	GeneratedAt              time.Time `json:"generated_at"`
}

type ValuationDebug struct {
	Signature      string        `json:"signature"`
	BaseSignature  string        `json:"base_signature,omitempty"`
	Status         string        `json:"status"`
	Reason         string        `json:"reason"`
	Valuation      *Valuation    `json:"valuation,omitempty"`
	Transactions   []Transaction `json:"transactions"`
	ActiveListings []Listing     `json:"active_listings"`
	RecentRawCount int           `json:"recent_raw_count"`
	GeneratedAt    time.Time     `json:"generated_at"`
}

type Thresholds struct {
	MinProfit        int64 `json:"min_profit"`
	MinMarginBPS     int   `json:"min_margin_bps"`
	MinConfidenceBPS int   `json:"min_confidence_bps"`
	MaxPurchasePrice int64 `json:"max_purchase_price"`
	MinVolume24h     int   `json:"min_volume_24h"`
}

type Opportunity struct {
	Listing    Listing   `json:"listing"`
	Valuation  Valuation `json:"valuation"`
	Profit     int64     `json:"profit"`
	MarginBPS  int       `json:"margin_bps"`
	DecisionNS int64     `json:"decision_ns"`
}

type OpportunityReport struct {
	Listings           int `json:"listings"`
	InvalidPrice       int `json:"invalid_price"`
	OverBudget         int `json:"over_budget"`
	Expired            int `json:"expired"`
	NoValuation        int `json:"no_valuation"`
	NoQuantityEvidence int `json:"no_quantity_evidence"`
	LowConfidence      int `json:"low_confidence"`
	LowVolume          int `json:"low_volume"`
	RiskBlocked        int `json:"risk_blocked"`
	Overflow           int `json:"overflow"`
	LowProfit          int `json:"low_profit"`
	LowMargin          int `json:"low_margin"`
	DuplicateSignature int `json:"duplicate_signature"`
	Qualified          int `json:"qualified"`
	Published          int `json:"published"`
}
