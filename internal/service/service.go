package service

import (
	"context"
	"crypto/sha256"
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

	"donut-network/internal/donutapi"
	"donut-network/internal/market"
	"donut-network/internal/orders"
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
	ObserverToken    string
	FabricToken      string
	DatabasePath     string
	AuctionFeeBPS    int
	OrderFeeBPS      int
	ListingPages     int
	CollectionPause  time.Duration
	FastInterval     time.Duration
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
	UnitReference   int64     `json:"unit_reference_value"`
	SingularUnitRef int64     `json:"singular_unit_reference"`
	QuantityUnitRef int64     `json:"quantity_unit_reference"`
	Profit          int64     `json:"profit"`
	MarginBPS       int       `json:"margin_bps"`
	ConfidenceBPS   int       `json:"confidence_bps"`
	Volume24h       int       `json:"volume_24h"`
	MarketVolume24h int       `json:"market_volume_24h"`
	PriceSellers    int       `json:"price_seller_count"`
	PriceBandLow    int64     `json:"price_band_low"`
	PriceBandHigh   int64     `json:"price_band_high"`
	SingularVolume  int       `json:"singular_volume_24h"`
	QuantityVolume  int       `json:"quantity_volume_24h"`
	ExpiresAt       time.Time `json:"expires_at,omitempty"`
	SearchCommand   string    `json:"search_command"`
	SellerCommand   string    `json:"seller_command"`
	ItemCommand     string    `json:"item_search_command"`
	ModelVersion    string    `json:"model_version"`
	PricingBasis    string    `json:"pricing_basis"`
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
	FastLastSuccessAt   time.Time      `json:"fast_last_success_at,omitempty"`
	FastDurationMS      float64        `json:"fast_duration_ms"`
	FastListingsFetched int            `json:"fast_listings_fetched"`
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
	cfg          Config
	upstream     Upstream
	history      History
	logger       *slog.Logger
	now          func() time.Time
	current      atomic.Pointer[Snapshot]
	engine       atomic.Pointer[market.Engine]
	version      atomic.Uint64
	cycleMu      sync.Mutex
	publishMu    sync.Mutex
	stored       []market.Transaction
	orders       *orders.System
	adminAuth    credential
	observerAuth credential
	fabricAuth   credential
}

type credential struct {
	enabled bool
	digest  [32]byte
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
	if cfg.FastInterval <= 0 {
		cfg.FastInterval = 250 * time.Millisecond
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
	adminAuth, observerAuth, fabricAuth := newCredential(cfg.ClientToken), newCredential(cfg.ObserverToken), newCredential(cfg.FabricToken)
	cfg.ClientToken, cfg.ObserverToken, cfg.FabricToken = "", "", ""
	orderSystem, err := orders.NewSystem(orders.Config{DatabasePath: cfg.DatabasePath, AuctionFeeBPS: cfg.AuctionFeeBPS, OrderFeeBPS: cfg.OrderFeeBPS, CandidateLimit: cfg.OpportunityLimit})
	if err != nil {
		return nil, fmt.Errorf("open order system: %w", err)
	}
	server := &Server{cfg: cfg, upstream: upstream, history: history, logger: logger, now: func() time.Time { return time.Now().UTC() }, orders: orderSystem,
		adminAuth: adminAuth, observerAuth: observerAuth, fabricAuth: fabricAuth}
	if history != nil {
		loaded, err := history.Load()
		if err != nil {
			_ = orderSystem.Close()
			return nil, fmt.Errorf("load transaction history: %w", err)
		}
		server.stored = loaded
	}
	initial := &Snapshot{GeneratedAt: server.now(), Status: Status{State: "starting", HistorySize: len(server.stored), Message: "waiting for first official API scan"}, Thresholds: cfg.Thresholds, Flips: []Flip{}}
	server.current.Store(initial)
	if len(server.stored) > 0 {
		engine := market.NewEngine()
		engine.AddTransactions(server.stored)
		server.engine.Store(engine)
	}
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
	s.updateCurrent(func(previous *Snapshot) {
		previous.Status.State = "collecting"
		previous.Status.CycleStartedAt = started
		previous.Status.Message = "reading transactions and active auction pages"
		previous.GeneratedAt = started
	})

	transactions, err := s.upstream.AllTransactionPages(ctx)
	if err != nil {
		return s.fail(started, fmt.Errorf("transactions: %w", err))
	}
	s.stored = state.Merge(s.stored, transactions, s.now(), 31*24*time.Hour, 100_000)
	if s.history != nil {
		if err := s.history.Save(s.stored); err != nil {
			return s.fail(started, fmt.Errorf("save transaction history: %w", err))
		}
	}
	engine := market.NewEngine()
	engine.AddTransactions(s.stored)
	// Keep broad active-book construction off the live engine so its 9,680-row
	// merge cannot pause newest-page detection. A fresh install without retained
	// history receives a sale-only seed while the first broad scan finishes.
	if s.engine.Load() == nil {
		fastSeed := market.NewEngine()
		fastSeed.AddTransactions(s.stored)
		s.engine.Store(fastSeed)
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
	s.engine.Store(engine)
	if err := s.orders.Refresh(ctx, engine); err != nil {
		s.logger.Warn("order candidates refresh failed", "error", err)
	}
	version := s.publishBroadSnapshot(Snapshot{GeneratedAt: now, Status: status, Thresholds: s.cfg.Thresholds,
		Analysis: analysis, Valuations: topValuations(marketSnapshot.Valuations, 25), Flips: flips})
	s.logger.Info("auction scan complete", "version", version, "transactions", len(transactions), "history", len(s.stored), "listings", len(listings), "valuations", len(marketSnapshot.Valuations), "flips", len(flips), "duration", now.Sub(started))
	return nil
}

func (s *Server) fail(started time.Time, collectionErr error) error {
	now := s.now()
	s.updateCurrent(func(current *Snapshot) {
		current.GeneratedAt = now
		current.Status.State = "error"
		current.Status.CycleStartedAt = started
		current.Status.CycleCompletedAt = now
		current.Status.CycleDurationMS = float64(now.Sub(started)) / float64(time.Millisecond)
		current.Status.NextCollectionAt = now.Add(s.cfg.CollectionPause)
		current.Status.Message = safeMessage(collectionErr)
		current.Status.API = s.upstream.Stats()
	})
	s.logger.Error("auction scan failed", "error", collectionErr)
	return collectionErr
}

func (s *Server) RunCollector(ctx context.Context) {
	go s.runBroadCollector(ctx)
	go s.runOrderMaintenance(ctx)
	s.runFastCollector(ctx)
}

func (s *Server) runOrderMaintenance(ctx context.Context) {
	if err := s.orders.Cleanup(ctx); err != nil {
		s.logger.Warn("order maintenance cleanup", "error", err)
	}
	if path, err := s.orders.Backup(ctx); err != nil {
		s.logger.Warn("order database backup", "error", err)
	} else if path != "" {
		s.logger.Info("order database backup complete", "path", path)
	}
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.orders.Cleanup(ctx); err != nil {
				s.logger.Warn("order maintenance cleanup", "error", err)
			}
			if path, err := s.orders.Backup(ctx); err != nil {
				s.logger.Warn("order database backup", "error", err)
			} else if path != "" {
				s.logger.Info("order database backup complete", "path", path)
			}
		}
	}
}

func (s *Server) runBroadCollector(ctx context.Context) {
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

func (s *Server) runFastCollector(ctx context.Context) {
	for {
		_ = s.CollectFastOnce(ctx)
		timer := time.NewTimer(s.cfg.FastInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// CollectFastOnce scans only the newest page and publishes against the latest
// completed-sale model. It is deliberately independent of the broad collector.
func (s *Server) CollectFastOnce(ctx context.Context) error {
	started := s.now()
	listings, err := s.upstream.AuctionPage(ctx, 1, "", "recently_listed")
	if err != nil {
		s.logger.Warn("fast auction refresh failed", "error", err)
		return err
	}
	engine := s.engine.Load()
	if engine == nil {
		return nil
	}
	engineVersion := engine.Version()
	observed, _ := engine.ObserveBatch(listings)
	previous := s.Snapshot()
	analysis, valuations, flips := previous.Analysis, previous.Valuations, previous.Flips
	valuationCount := previous.Status.ValuationCount
	if engine.Version() != engineVersion {
		opportunities, newestAnalysis := engine.AnalyzeListings(observed, s.cfg.Thresholds, s.cfg.OpportunityLimit)
		analysis = newestAnalysis
		flips = make([]Flip, 0, len(opportunities))
		for _, opportunity := range opportunities {
			flips = append(flips, mapFlip(opportunity))
		}
		marketSnapshot := engine.Snapshot()
		valuationCount = len(marketSnapshot.Valuations)
		valuations = topValuations(marketSnapshot.Valuations, 25)
	}
	now := s.now()
	// A completed broad scan may replace the engine while this request is being
	// evaluated. Never let a result from that retired model replace the new feed.
	if s.engine.Load() != engine {
		return nil
	}
	if _, err := s.orders.RefreshIfDue(ctx, engine, 750*time.Millisecond); err != nil {
		s.logger.Warn("fast order candidates refresh failed", "error", err)
	}
	status := Status{ValuationCount: valuationCount, FlipCount: len(flips), API: s.upstream.Stats()}
	version := s.publishFastSnapshot(Snapshot{GeneratedAt: now, Status: status, Thresholds: s.cfg.Thresholds,
		Analysis: analysis, Valuations: valuations, Flips: flips}, started, now, len(listings))
	s.logger.Debug("fast auction refresh complete", "version", version, "listings", len(listings), "flips", len(flips), "duration", now.Sub(started))
	return nil
}

func (s *Server) updateCurrent(update func(*Snapshot)) {
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	current := *s.current.Load()
	update(&current)
	s.current.Store(&current)
}

func (s *Server) publishBroadSnapshot(snapshot Snapshot) uint64 {
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	current := s.current.Load()
	snapshot.Status.FastLastSuccessAt = current.Status.FastLastSuccessAt
	snapshot.Status.FastDurationMS = current.Status.FastDurationMS
	snapshot.Status.FastListingsFetched = current.Status.FastListingsFetched
	// A fast refresh can finish after the broad collector built its result but
	// before this lock is acquired. Preserve that newer feed while still
	// publishing the completed broad-scan counters.
	if current.GeneratedAt.After(snapshot.GeneratedAt) {
		snapshot.GeneratedAt = current.GeneratedAt
		snapshot.Analysis = current.Analysis
		snapshot.Valuations = current.Valuations
		snapshot.Flips = current.Flips
		snapshot.Status.LastSuccessAt = current.Status.LastSuccessAt
		snapshot.Status.ValuationCount = current.Status.ValuationCount
		snapshot.Status.FlipCount = current.Status.FlipCount
	}
	version := s.version.Add(1)
	snapshot.Version = version
	s.current.Store(&snapshot)
	return version
}

func (s *Server) publishFastSnapshot(snapshot Snapshot, started, now time.Time, listingCount int) uint64 {
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	status := s.current.Load().Status
	status.State = "ready"
	status.Message = "live newest-page refresh; broad valuation scan runs in background"
	status.LastSuccessAt = now
	status.FastLastSuccessAt = now
	status.FastDurationMS = float64(now.Sub(started)) / float64(time.Millisecond)
	status.FastListingsFetched = listingCount
	status.ValuationCount = snapshot.Status.ValuationCount
	status.FlipCount = snapshot.Status.FlipCount
	status.API = snapshot.Status.API
	snapshot.Status = status
	version := s.version.Add(1)
	snapshot.Version = version
	s.current.Store(&snapshot)
	return version
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/v1/flips", s.authorize(s.flips))
	mux.HandleFunc("GET /api/v1/debug", s.authorize(s.debugJSON))
	mux.HandleFunc("GET /api/v1/debug/valuation", s.authorize(s.debugValuation))
	mux.HandleFunc("POST /api/v1/observers/register", s.authorizeWith(s.observerAuth, s.observerRegister))
	mux.HandleFunc("GET /api/v1/observers/tasks", s.authorizeWith(s.observerAuth, s.observerTasks))
	mux.HandleFunc("POST /api/v1/observers/heartbeat", s.authorizeWith(s.observerAuth, s.observerHeartbeat))
	mux.HandleFunc("POST /api/v1/observers/order-scans", s.authorizeWith(s.observerAuth, s.observerOrderScans))
	mux.HandleFunc("POST /api/v1/observers/task-result", s.authorizeWith(s.observerAuth, s.observerTaskResult))
	mux.HandleFunc("GET /api/v1/candidates", s.authorizeWith(s.fabricAuth, s.candidateFeed))
	mux.HandleFunc("POST /api/v1/watches", s.authorizeWith(s.fabricAuth, s.addWatch))
	mux.HandleFunc("DELETE /api/v1/watches/{id}", s.authorizeWith(s.fabricAuth, s.deleteWatch))
	mux.HandleFunc("POST /api/v1/client/diagnostics", s.authorizeWith(s.fabricAuth, s.clientDiagnostics))
	mux.HandleFunc("GET /order-auction-flipper", s.authorize(s.orderAuctionPage))
	mux.HandleFunc("GET /order-auction-flipper/debug", s.authorize(s.orderAuctionDebugPage))
	mux.HandleFunc("POST /order-auction-flipper/watch", s.authorize(s.dashboardWatch))
	mux.HandleFunc("GET /", s.authorize(s.debugPage))
	return securityHeaders(mux)
}

func (s *Server) HTTPServer() *http.Server {
	return &http.Server{Addr: s.cfg.Address, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
}

func (s *Server) Close() error { return s.orders.Close() }

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

func (s *Server) orderAuctionPage(w http.ResponseWriter, r *http.Request) {
	data := s.simpleOrderPageData()
	etag := `"order-page-` + uintString(data.Version) + `"`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if err := orderAuctionSimpleTemplate.Execute(w, data); err != nil {
		s.logger.Warn("render order recommendations", "error", err)
	}
}

func (s *Server) simpleOrderPageData() simpleOrderPageData {
	feed := s.orders.CandidateFeed()
	data := simpleOrderPageData{Version: feed.Version, GeneratedAt: feed.GeneratedAt, Ready: make([]orders.Candidate, 0, 20), Research: make([]orders.Candidate, 0, 10)}
	for _, candidate := range feed.Candidates {
		if candidate.PriorityRank <= 0 || candidate.Route != "ORDER_TO_AUCTION" {
			continue
		}
		switch candidate.State {
		case "READY":
			data.ReadyCount++
			if candidate.OrderTier == "actionable" {
				data.CoreCount++
			} else {
				data.FillerCount++
			}
			if len(data.Ready) < cap(data.Ready) {
				candidate.PriorityRank = len(data.Ready) + 1
				data.Ready = append(data.Ready, candidate)
			}
		case "RESEARCH":
			data.ResearchCount++
			if len(data.Research) < cap(data.Research) {
				candidate.PriorityRank = len(data.Research) + 1
				data.Research = append(data.Research, candidate)
			}
		}
	}
	return data
}

func (s *Server) orderAuctionDebugPage(w http.ResponseWriter, r *http.Request) {
	data, err := s.loadOrderPageData(r.Context())
	if err != nil {
		s.orderError(w, "load order debug", err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := orderAuctionTemplateV2.Execute(w, data); err != nil {
		s.logger.Warn("render order-auction debug page", "error", err)
	}
}

func (s *Server) loadOrderPageData(ctx context.Context) (orderPageData, error) {
	debug, err := s.orders.Debug(ctx)
	if err != nil {
		return orderPageData{}, err
	}
	priority := make([]orders.Candidate, 0, 30)
	ready := make([]orders.Candidate, 0, 20)
	research := make([]orders.Candidate, 0, 10)
	immediate := make([]orders.Candidate, 0, 20)
	blocked := make([]orders.Candidate, 0, 50)
	readyCount, researchCount := 0, 0
	orderRank, immediateRank := 0, 0
	for _, candidate := range debug.Candidates {
		if candidate.PriorityRank > 0 && candidate.Route == "ORDER_TO_AUCTION" {
			if candidate.State == "READY" {
				readyCount++
				if len(ready) < 20 {
					value := candidate
					value.PriorityRank = len(ready) + 1
					ready = append(ready, value)
				}
			} else if candidate.State == "RESEARCH" {
				researchCount++
				if len(research) < 10 {
					value := candidate
					value.PriorityRank = len(research) + 1
					research = append(research, value)
				}
			}
			if len(priority) < 30 {
				orderRank++
				candidate.PriorityRank = orderRank
				priority = append(priority, candidate)
			}
			continue
		}
		if candidate.PriorityRank > 0 && candidate.Route == "AUCTION_TO_ORDER" && len(immediate) < 20 {
			immediateRank++
			candidate.PriorityRank = immediateRank
			immediate = append(immediate, candidate)
			continue
		}
		if len(blocked) < 50 {
			blocked = append(blocked, candidate)
		}
	}
	return orderPageData{Auction: s.Snapshot(), Orders: debug, Ready: ready, Research: research,
		Priority: priority, Immediate: immediate, Blocked: blocked, ReadyCount: readyCount, ResearchCount: researchCount}, nil
}

func (s *Server) authorize(next http.HandlerFunc) http.HandlerFunc {
	return s.authorizeWith(s.adminAuth, next)
}

func (s *Server) authorizeWith(auth credential, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if auth.enabled {
			provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			digest := sha256.Sum256([]byte(provided))
			if subtle.ConstantTimeCompare(digest[:], auth.digest[:]) != 1 {
				w.Header().Set("WWW-Authenticate", "Bearer")
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid client token"})
				return
			}
		}
		next(w, r)
	}
}

func newCredential(token string) credential {
	if token == "" {
		return credential{}
	}
	return credential{enabled: true, digest: sha256.Sum256([]byte(token))}
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

func ValidateScopedTokens(address, adminToken, observerToken, fabricToken string) error {
	if observerToken != "" && !validToken(observerToken) {
		return errors.New("DN_OBSERVER_TOKEN must be 16-512 printable ASCII characters without spaces")
	}
	if fabricToken != "" && !validToken(fabricToken) {
		return errors.New("DN_FABRIC_TOKEN must be 16-512 printable ASCII characters without spaces")
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid DN_ADDRESS: %w", err)
	}
	ip := net.ParseIP(host)
	local := host == "localhost" || (ip != nil && ip.IsLoopback())
	if !local && (observerToken == "" || fabricToken == "") {
		return errors.New("DN_OBSERVER_TOKEN and DN_FABRIC_TOKEN are required when DN_ADDRESS is not loopback")
	}
	nonempty := []string{}
	for _, token := range []string{adminToken, observerToken, fabricToken} {
		if token != "" {
			nonempty = append(nonempty, token)
		}
	}
	for left := range nonempty {
		for right := left + 1; right < len(nonempty); right++ {
			if subtle.ConstantTimeCompare([]byte(nonempty[left]), []byte(nonempty[right])) == 1 {
				return errors.New("administrator, observer, and Fabric tokens must be distinct")
			}
		}
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
	itemCommand := "/ah " + itemSearchID(listing.Item.ID, name)
	sellerCommand := sellerSearchCommand(listing.SellerName)
	primaryCommand := sellerCommand
	if primaryCommand == "" {
		primaryCommand = itemCommand
		sellerCommand = itemCommand
	}
	return Flip{
		Key: key, AuctionID: listing.AuthoritativeID, ItemID: listing.Item.ID, ItemName: name,
		Quantity: max(1, listing.Item.Quantity), Seller: listing.SellerName, Price: listing.TotalPrice,
		ReferenceValue: listing.TotalPrice + opportunity.Profit, UnitReference: opportunity.Valuation.QuickSellValue,
		SingularUnitRef: opportunity.Valuation.SingularQuickSell, QuantityUnitRef: opportunity.Valuation.QuantityQuickSell,
		Profit: opportunity.Profit, MarginBPS: opportunity.MarginBPS,
		ConfidenceBPS: opportunity.Valuation.ConfidenceBPS, Volume24h: opportunity.Valuation.Volume24h,
		MarketVolume24h: opportunity.Valuation.MarketVolume24h,
		PriceSellers:    opportunity.Valuation.PriceSellerCount,
		PriceBandLow:    opportunity.Valuation.PriceBandLow, PriceBandHigh: opportunity.Valuation.PriceBandHigh,
		SingularVolume: opportunity.Valuation.SingularVolume24h, QuantityVolume: opportunity.Valuation.QuantityVolume24h,
		ExpiresAt: listing.ExpiresAt, SearchCommand: primaryCommand, SellerCommand: sellerCommand,
		ItemCommand: itemCommand, ModelVersion: opportunity.Valuation.ModelVersion,
		PricingBasis:    opportunity.Valuation.FallbackLevel,
		ExpectedSellMin: opportunity.Valuation.ExpectedSellMinutes, RiskFlags: append([]string(nil), opportunity.Valuation.RiskFlags...),
	}
}

func itemSearchID(itemID, fallbackName string) string {
	value := strings.ToLower(strings.TrimSpace(itemID))
	if separator := strings.LastIndexByte(value, ':'); separator >= 0 {
		value = value[separator+1:]
	}
	if value == "" {
		value = strings.ToLower(strings.TrimSpace(fallbackName))
	}
	value = strings.Map(func(character rune) rune {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_' || character == '-' {
			return character
		}
		return '_'
	}, value)
	value = strings.Trim(value, "_")
	for strings.Contains(value, "__") {
		value = strings.ReplaceAll(value, "__", "_")
	}
	if len(value) > 48 {
		value = value[:48]
	}
	if value == "" {
		return "item"
	}
	return value
}

func sellerSearchCommand(seller string) string {
	seller = strings.TrimSpace(seller)
	if len(seller) < 1 || len(seller) > 16 {
		return ""
	}
	for _, character := range seller {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' {
			continue
		}
		return ""
	}
	return "/ah " + seller
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
	"clock": func(value time.Time) string {
		if value.IsZero() {
			return "never"
		}
		return value.Local().Format("15:04:05.000")
	},
}).Parse(debugHTML))

const debugHTML = `<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<meta http-equiv="refresh" content="1"><title>Donut auction debug</title>
<style>body{font:14px monospace;max-width:1100px;margin:24px auto;padding:0 16px;color:#ddd;background:#111}h1,h2{color:#fff}table{border-collapse:collapse;width:100%;margin-bottom:24px}th,td{text-align:left;padding:7px;border-bottom:1px solid #333}.ready{color:#7ee787}.error{color:#ff7b72}.collecting{color:#d2a8ff}code,a{color:#a5d6ff}.muted{color:#999}.funnel{line-height:1.7}</style>
</head><body><nav><a href="/">Auction API debug</a> · <a href="/order-auction-flipper">Order-auction flipper</a></nav><h1>Donut auction API debug</h1>
<p>Status: <strong class="{{.Status.State}}">{{.Status.State}}</strong> · snapshot {{.Version}} · {{.Status.Message}}</p>
<p>Listings {{.Status.ListingsFetched}} · latest transactions {{.Status.TransactionsFetched}} · retained history {{.Status.HistorySize}} · valuations {{.Status.ValuationCount}} · flips {{.Status.FlipCount}}</p>
<p>Fast lane: {{.Status.FastListingsFetched}} newest rows · last publish {{clock .Status.FastLastSuccessAt}} · {{printf "%.0f" .Status.FastDurationMS}}ms upstream-to-feed</p>
<p>API requests {{.Status.API.Requests}} · errors {{.Status.API.Errors}} · retries {{.Status.API.Retries}} · last latency {{printf "%.0f" .Status.API.LastLatencyMS}}ms</p>
<p>Thresholds: profit ≥ {{money .Thresholds.MinProfit}} · margin ≥ {{pct .Thresholds.MinMarginBPS}} · confidence ≥ {{pct .Thresholds.MinConfidenceBPS}} · 24h sales near target ≥ {{.Thresholds.MinVolume24h}}</p>
<p class="muted">Refreshes every second. The API key is backend-only and is never rendered.</p>
<h2>Decision funnel</h2><p class="funnel">{{.Analysis.Listings}} listings → no valuation {{.Analysis.NoValuation}} · no singular/exact-quantity evidence {{.Analysis.NoQuantityEvidence}} · low confidence {{.Analysis.LowConfidence}} · low volume {{.Analysis.LowVolume}} · risk blocked {{.Analysis.RiskBlocked}} · low profit {{.Analysis.LowProfit}} · low margin {{.Analysis.LowMargin}} · over budget {{.Analysis.OverBudget}} · expired {{.Analysis.Expired}} · duplicate signature {{.Analysis.DuplicateSignature}} → <strong>{{.Analysis.Published}} published</strong></p>
<h2>Current opportunities</h2><table><thead><tr><th>Item</th><th>Price</th><th>Unit refs (1 / exact / used)</th><th>Total ref</th><th>Profit</th><th>Margin</th><th>Confidence</th><th>24h near target / all</th><th>Basis</th><th>Seller / item routes</th></tr></thead><tbody>
{{range .Flips}}<tr><td>{{.Quantity}}× {{.ItemName}}</td><td>{{money .Price}}</td><td>{{money .SingularUnitRef}} / {{money .QuantityUnitRef}} / <strong>{{money .UnitReference}}</strong></td><td>{{money .ReferenceValue}}</td><td>{{money .Profit}}</td><td>{{pct .MarginBPS}}</td><td>{{pct .ConfidenceBPS}}</td><td>{{.Volume24h}} near {{money .PriceBandLow}}–{{money .PriceBandHigh}} from {{.PriceSellers}} sellers / {{.MarketVolume24h}} all</td><td>{{.PricingBasis}}</td><td>seller <code>{{.SellerCommand}}</code><br>item <code>{{.ItemCommand}}</code></td></tr>{{else}}<tr><td colspan="10">No flips currently pass the configured safety thresholds.</td></tr>{{end}}</tbody></table>
<h2>Highest-volume valuations</h2><table><thead><tr><th>Signature</th><th>Quick sell</th><th>Fair</th><th>Confidence</th><th>24h near target / all</th><th>Samples</th><th>Sell time</th><th>Risk flags</th></tr></thead><tbody>
{{range .Valuations}}<tr><td><a href="/api/v1/debug/valuation?signature={{urlquery .Signature}}">{{.Signature}}</a></td><td>{{money .QuickSellValue}}</td><td>{{money .FairValue}}</td><td>{{pct .ConfidenceBPS}}</td><td>{{.Volume24h}} near {{money .PriceBandLow}}–{{money .PriceBandHigh}} from {{.PriceSellerCount}} sellers / {{.MarketVolume24h}} all</td><td>{{.SampleCount}}</td><td>{{.ExpectedSellMinutes}}m</td><td>{{range .RiskFlags}}{{.}} {{end}}</td></tr>{{else}}<tr><td colspan="8">No completed-sale model is ready yet.</td></tr>{{end}}</tbody></table>
</body></html>`
