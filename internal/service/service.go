package service

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"donut-network/internal/donutapi"
	"donut-network/internal/market"
	"donut-network/internal/state"
)

const auctionPageSize = 44

type Upstream interface {
	AllTransactionPages(context.Context) ([]market.Transaction, error)
	AuctionPage(context.Context, int, string, string) ([]market.Listing, error)
	Stats() donutapi.Stats
}

type History interface {
	Load() ([]market.Transaction, error)
	Save([]market.Transaction) error
}

type Config struct {
	Address          string
	ClientToken      string
	ListingPages     int
	CollectionPause  time.Duration
	OpportunityLimit int
	Thresholds       market.Thresholds
}

type Flip struct {
	Key             string    `json:"key"`
	AuctionID       string    `json:"auction_id,omitempty"`
	ItemID          string    `json:"item_id"`
	ItemName        string    `json:"item_name"`
	Quantity        int       `json:"quantity"`
	Seller          string    `json:"seller"`
	Price           int64     `json:"price"`
	ReferenceValue  int64     `json:"reference_value"`
	Profit          int64     `json:"profit"`
	MarginBPS       int       `json:"margin_bps"`
	ConfidenceBPS   int       `json:"confidence_bps"`
	Volume24h       int       `json:"volume_24h"`
	ExpiresAt       time.Time `json:"expires_at,omitempty"`
	SearchCommand   string    `json:"search_command"`
	ModelVersion    string    `json:"model_version"`
	ExpectedSellMin int       `json:"expected_sell_minutes"`
	RiskFlags       []string  `json:"risk_flags,omitempty"`
}

type Status struct {
	State               string         `json:"state"`
	CycleStartedAt      time.Time      `json:"cycle_started_at,omitempty"`
	CycleCompletedAt    time.Time      `json:"cycle_completed_at,omitempty"`
	LastSuccessAt       time.Time      `json:"last_success_at,omitempty"`
	NextCollectionAt    time.Time      `json:"next_collection_at,omitempty"`
	CycleDurationMS     float64        `json:"cycle_duration_ms"`
	ListingsFetched     int            `json:"listings_fetched"`
	TransactionsFetched int            `json:"transactions_fetched"`
	HistorySize         int            `json:"history_size"`
	ValuationCount      int            `json:"valuation_count"`
	FlipCount           int            `json:"flip_count"`
	Message             string         `json:"message,omitempty"`
	API                 donutapi.Stats `json:"api"`
}

type Snapshot struct {
	Version     uint64                   `json:"version"`
	GeneratedAt time.Time                `json:"generated_at"`
	Status      Status                   `json:"status"`
	Thresholds  market.Thresholds        `json:"thresholds"`
	Analysis    market.OpportunityReport `json:"analysis"`
	Valuations  []market.Valuation       `json:"top_valuations"`
	Flips       []Flip                   `json:"flips"`
}

type Server struct {
	cfg      Config
	upstream Upstream
	history  History
	logger   *slog.Logger
	now      func() time.Time
	current  atomic.Pointer[Snapshot]
	engine   atomic.Pointer[market.Engine]
	version  atomic.Uint64
	cycleMu  sync.Mutex
	stored   []market.Transaction
}

func New(cfg Config, upstream Upstream, history History, logger *slog.Logger) (*Server, error) {
	if upstream == nil {
		return nil, errors.New("upstream is required")
	}
	if cfg.Address == "" {
		cfg.Address = "127.0.0.1:8080"
	}
	if cfg.ListingPages <= 0 {
		cfg.ListingPages = 220
	}
	if cfg.ListingPages > 220 {
		return nil, errors.New("listing pages must be between 1 and 220")
	}
	if cfg.CollectionPause <= 0 {
		cfg.CollectionPause = 5 * time.Second
	}
	if cfg.OpportunityLimit <= 0 {
		cfg.OpportunityLimit = 100
	}
	if cfg.Thresholds.MinProfit <= 0 {
		cfg.Thresholds.MinProfit = 100_000
	}
	if cfg.Thresholds.MinMarginBPS <= 0 {
		cfg.Thresholds.MinMarginBPS = 1_000
	}
	if cfg.Thresholds.MinConfidenceBPS <= 0 {
		cfg.Thresholds.MinConfidenceBPS = 5_000
	}
	if cfg.Thresholds.MinVolume24h <= 0 {
		cfg.Thresholds.MinVolume24h = 2
	}
	if logger == nil {
		logger = slog.Default()
	}
	server := &Server{cfg: cfg, upstream: upstream, history: history, logger: logger, now: func() time.Time { return time.Now().UTC() }}
	if history != nil {
		loaded, err := history.Load()
		if err != nil {
			return nil, fmt.Errorf("load transaction history: %w", err)
		}
		server.stored = loaded
	}
	initial := &Snapshot{GeneratedAt: server.now(), Status: Status{State: "starting", HistorySize: len(server.stored), Message: "waiting for first official API scan"}, Thresholds: cfg.Thresholds, Flips: []Flip{}}
	server.current.Store(initial)
	return server, nil
}

func (s *Server) Snapshot() Snapshot {
	current := s.current.Load()
	copy := *current
	// Keep initialized empty slices non-nil so the HTTP contract consistently
	// emits JSON arrays ([]) rather than null while the first scan is running.
	copy.Flips = append(make([]Flip, 0, len(current.Flips)), current.Flips...)
	copy.Valuations = append(make([]market.Valuation, 0, len(current.Valuations)), current.Valuations...)
	return copy
}

func (s *Server) CollectOnce(ctx context.Context) error {
	s.cycleMu.Lock()
	defer s.cycleMu.Unlock()
	started := s.now()
	previous := s.Snapshot()
	previous.Status.State = "collecting"
	previous.Status.CycleStartedAt = started
	previous.Status.Message = "reading transactions and active auction pages"
	previous.GeneratedAt = started
	s.current.Store(&previous)

	transactions, err := s.upstream.AllTransactionPages(ctx)
	if err != nil {
		return s.fail(started, fmt.Errorf("transactions: %w", err))
	}
	listings := make([]market.Listing, 0, 4096)
	reachedCap := true
	for page := 1; page <= s.cfg.ListingPages; page++ {
		batch, pageErr := s.upstream.AuctionPage(ctx, page, "", "recently_listed")
		if pageErr != nil {
			return s.fail(started, fmt.Errorf("auction page %d: %w", page, pageErr))
		}
		listings = append(listings, batch...)
		if len(batch) < auctionPageSize {
			reachedCap = false
			break
		}
	}

	s.stored = state.Merge(s.stored, transactions, s.now(), 31*24*time.Hour, 100_000)
	if s.history != nil {
		if err := s.history.Save(s.stored); err != nil {
			return s.fail(started, fmt.Errorf("save transaction history: %w", err))
		}
	}
	engine := market.NewEngine()
	engine.AddTransactions(s.stored)
	engine.ObserveBatch(listings)
	marketSnapshot := engine.Snapshot()
	opportunities, analysis := engine.AnalyzeOpportunities(s.cfg.Thresholds, s.cfg.OpportunityLimit)
	flips := make([]Flip, 0, len(opportunities))
	for _, opportunity := range opportunities {
		flips = append(flips, mapFlip(opportunity))
	}
	now := s.now()
	message := "official API scan complete"
	if reachedCap {
		message = "recent-listing window complete at the configured latency cap"
	}
	status := Status{
		State: "ready", CycleStartedAt: started, CycleCompletedAt: now, LastSuccessAt: now,
		NextCollectionAt: now.Add(s.cfg.CollectionPause), CycleDurationMS: float64(now.Sub(started)) / float64(time.Millisecond),
		ListingsFetched: len(listings), TransactionsFetched: len(transactions), HistorySize: len(s.stored),
		ValuationCount: len(marketSnapshot.Valuations), FlipCount: len(flips), Message: message, API: s.upstream.Stats(),
	}
	version := s.version.Add(1)
	s.engine.Store(engine)
	s.current.Store(&Snapshot{Version: version, GeneratedAt: now, Status: status, Thresholds: s.cfg.Thresholds,
		Analysis: analysis, Valuations: topValuations(marketSnapshot.Valuations, 25), Flips: flips})
	s.logger.Info("auction scan complete", "version", version, "transactions", len(transactions), "history", len(s.stored), "listings", len(listings), "valuations", len(marketSnapshot.Valuations), "flips", len(flips), "duration", now.Sub(started))
	return nil
}

func (s *Server) fail(started time.Time, collectionErr error) error {
	current := s.Snapshot()
	now := s.now()
	current.GeneratedAt = now
	current.Status.State = "error"
	current.Status.CycleStartedAt = started
	current.Status.CycleCompletedAt = now
	current.Status.CycleDurationMS = float64(now.Sub(started)) / float64(time.Millisecond)
	current.Status.NextCollectionAt = now.Add(s.cfg.CollectionPause)
	current.Status.Message = safeMessage(collectionErr)
	current.Status.API = s.upstream.Stats()
	s.current.Store(&current)
	s.logger.Error("auction scan failed", "error", collectionErr)
	return collectionErr
}

func (s *Server) RunCollector(ctx context.Context) {
	for {
		_ = s.CollectOnce(ctx)
		timer := time.NewTimer(s.cfg.CollectionPause)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/v1/flips", s.authorize(s.flips))
	mux.HandleFunc("GET /api/v1/debug", s.authorize(s.debugJSON))
	mux.HandleFunc("GET /api/v1/debug/valuation", s.authorize(s.debugValuation))
	mux.HandleFunc("GET /", s.authorize(s.debugPage))
	return securityHeaders(mux)
}

func (s *Server) HTTPServer() *http.Server {
	return &http.Server{Addr: s.cfg.Address, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	snapshot := s.Snapshot()
	status := http.StatusOK
	if snapshot.Status.LastSuccessAt.IsZero() {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{"status": snapshot.Status.State, "version": snapshot.Version, "last_success_at": snapshot.Status.LastSuccessAt, "message": snapshot.Status.Message})
}

func (s *Server) flips(w http.ResponseWriter, r *http.Request) {
	snapshot := s.Snapshot()
	// Collection/error state can change without a successful market version.
	// Include it so conditional clients cannot remain falsely "ready" after a failure.
	etag := fmt.Sprintf("\"%d-%s\"", snapshot.Version, snapshot.Status.State)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": snapshot.Version, "generated_at": snapshot.GeneratedAt, "status": snapshot.Status.State, "flips": snapshot.Flips})
}

func (s *Server) debugJSON(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Snapshot())
}

func (s *Server) debugValuation(w http.ResponseWriter, r *http.Request) {
	signature := strings.TrimSpace(r.URL.Query().Get("signature"))
	if signature == "" || len(signature) > 2_048 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "signature is required and must be at most 2048 bytes"})
		return
	}
	engine := s.engine.Load()
	if engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no completed market scan"})
		return
	}
	writeJSON(w, http.StatusOK, engine.Explain(signature))
}

func (s *Server) debugPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := debugTemplate.Execute(w, s.Snapshot()); err != nil {
		s.logger.Warn("render debug page", "error", err)
	}
}

func (s *Server) authorize(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.ClientToken != "" {
			provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if len(provided) != len(s.cfg.ClientToken) || subtle.ConstantTimeCompare([]byte(provided), []byte(s.cfg.ClientToken)) != 1 {
				w.Header().Set("WWW-Authenticate", "Bearer")
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid client token"})
				return
			}
		}
		next(w, r)
	}
}

func ValidateBind(address, token string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid DN_ADDRESS: %w", err)
	}
	ip := net.ParseIP(host)
	local := host == "localhost" || (ip != nil && ip.IsLoopback())
	if token != "" && !validToken(token) {
		return errors.New("DN_CLIENT_TOKEN must be 16-512 printable ASCII characters without spaces")
	}
	if !local && token == "" {
		return errors.New("DN_CLIENT_TOKEN is required when DN_ADDRESS is not loopback")
	}
	return nil
}

func validToken(token string) bool {
	if len(token) < 16 || len(token) > 512 {
		return false
	}
	for _, character := range token {
		if character < 33 || character > 126 {
			return false
		}
	}
	return true
}

func mapFlip(opportunity market.Opportunity) Flip {
	listing := opportunity.Listing
	name := strings.TrimSpace(listing.Item.DisplayName)
	if name == "" {
		name = strings.TrimPrefix(listing.Item.ID, "minecraft:")
		name = strings.ReplaceAll(name, "_", " ")
	}
	key := listing.AuthoritativeID
	if key == "" {
		key = listing.Fingerprint
	}
	return Flip{
		Key: key, AuctionID: listing.AuthoritativeID, ItemID: listing.Item.ID, ItemName: name,
		Quantity: max(1, listing.Item.Quantity), Seller: listing.SellerName, Price: listing.TotalPrice,
		ReferenceValue: listing.TotalPrice + opportunity.Profit, Profit: opportunity.Profit, MarginBPS: opportunity.MarginBPS,
		ConfidenceBPS: opportunity.Valuation.ConfidenceBPS, Volume24h: opportunity.Valuation.Volume24h,
		ExpiresAt: listing.ExpiresAt, SearchCommand: "/ah " + safeSearch(name), ModelVersion: opportunity.Valuation.ModelVersion,
		ExpectedSellMin: opportunity.Valuation.ExpectedSellMinutes, RiskFlags: append([]string(nil), opportunity.Valuation.RiskFlags...),
	}
}

func safeSearch(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) || r == '_' || r == '-' {
			return r
		}
		return -1
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > 48 {
		value = string(runes[:48])
	}
	if value == "" {
		return "item"
	}
	return value
}

func topValuations(values map[string]market.Valuation, limit int) []market.Valuation {
	out := make([]market.Valuation, 0, len(values))
	for _, valuation := range values {
		valuation.RiskFlags = append([]string(nil), valuation.RiskFlags...)
		out = append(out, valuation)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Volume24h == out[j].Volume24h {
			if out[i].ConfidenceBPS == out[j].ConfidenceBPS {
				return out[i].Signature < out[j].Signature
			}
			return out[i].ConfidenceBPS > out[j].ConfidenceBPS
		}
		return out[i].Volume24h > out[j].Volume24h
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func safeMessage(err error) string {
	message := strings.Join(strings.Fields(err.Error()), " ")
	if len(message) > 300 {
		message = message[:300]
	}
	return message
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; connect-src 'self'; script-src 'unsafe-inline'")
		next.ServeHTTP(w, r)
	})
}

var debugTemplate = template.Must(template.New("debug").Funcs(template.FuncMap{
	"money": func(value int64) string { return fmt.Sprintf("$%d", value) },
	"pct":   func(value int) string { return fmt.Sprintf("%.1f%%", float64(value)/100) },
}).Parse(debugHTML))

const debugHTML = `<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<meta http-equiv="refresh" content="5"><title>Donut auction debug</title>
<style>body{font:14px monospace;max-width:1100px;margin:24px auto;padding:0 16px;color:#ddd;background:#111}h1,h2{color:#fff}table{border-collapse:collapse;width:100%;margin-bottom:24px}th,td{text-align:left;padding:7px;border-bottom:1px solid #333}.ready{color:#7ee787}.error{color:#ff7b72}.collecting{color:#d2a8ff}code,a{color:#a5d6ff}.muted{color:#999}.funnel{line-height:1.7}</style>
</head><body><h1>Donut auction API debug</h1>
<p>Status: <strong class="{{.Status.State}}">{{.Status.State}}</strong> · snapshot {{.Version}} · {{.Status.Message}}</p>
<p>Listings {{.Status.ListingsFetched}} · latest transactions {{.Status.TransactionsFetched}} · retained history {{.Status.HistorySize}} · valuations {{.Status.ValuationCount}} · flips {{.Status.FlipCount}}</p>
<p>API requests {{.Status.API.Requests}} · errors {{.Status.API.Errors}} · retries {{.Status.API.Retries}} · last latency {{printf "%.0f" .Status.API.LastLatencyMS}}ms</p>
<p>Thresholds: profit ≥ {{money .Thresholds.MinProfit}} · margin ≥ {{pct .Thresholds.MinMarginBPS}} · confidence ≥ {{pct .Thresholds.MinConfidenceBPS}} · 24h sales ≥ {{.Thresholds.MinVolume24h}}</p>
<p class="muted">Refreshes every five seconds. The API key is backend-only and is never rendered.</p>
<h2>Decision funnel</h2><p class="funnel">{{.Analysis.Listings}} listings → no valuation {{.Analysis.NoValuation}} · low confidence {{.Analysis.LowConfidence}} · low volume {{.Analysis.LowVolume}} · risk blocked {{.Analysis.RiskBlocked}} · low profit {{.Analysis.LowProfit}} · low margin {{.Analysis.LowMargin}} · over budget {{.Analysis.OverBudget}} · expired {{.Analysis.Expired}} · duplicate signature {{.Analysis.DuplicateSignature}} → <strong>{{.Analysis.Published}} published</strong></p>
<h2>Current opportunities</h2><table><thead><tr><th>Item</th><th>Price</th><th>Reference</th><th>Profit</th><th>Margin</th><th>Confidence</th><th>24h sales</th><th>Command</th></tr></thead><tbody>
{{range .Flips}}<tr><td>{{.Quantity}}× {{.ItemName}}</td><td>{{money .Price}}</td><td>{{money .ReferenceValue}}</td><td>{{money .Profit}}</td><td>{{pct .MarginBPS}}</td><td>{{pct .ConfidenceBPS}}</td><td>{{.Volume24h}}</td><td><code>{{.SearchCommand}}</code></td></tr>{{else}}<tr><td colspan="8">No flips currently pass the configured safety thresholds.</td></tr>{{end}}</tbody></table>
<h2>Highest-volume valuations</h2><table><thead><tr><th>Signature</th><th>Quick sell</th><th>Fair</th><th>Confidence</th><th>24h sales</th><th>Samples</th><th>Sell time</th><th>Risk flags</th></tr></thead><tbody>
{{range .Valuations}}<tr><td><a href="/api/v1/debug/valuation?signature={{urlquery .Signature}}">{{.Signature}}</a></td><td>{{money .QuickSellValue}}</td><td>{{money .FairValue}}</td><td>{{pct .ConfidenceBPS}}</td><td>{{.Volume24h}}</td><td>{{.SampleCount}}</td><td>{{.ExpectedSellMinutes}}m</td><td>{{range .RiskFlags}}{{.}} {{end}}</td></tr>{{else}}<tr><td colspan="8">No completed-sale model is ready yet.</td></tr>{{end}}</tbody></table>
</body></html>`
