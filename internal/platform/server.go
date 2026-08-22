package platform

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"donut-network/internal/market"
	"donut-network/internal/network"
	"github.com/gorilla/websocket"
)

type Repository interface {
	StoreTransactions(context.Context, []market.Transaction) error
	UpsertListings(context.Context, []market.Listing) error
	StoreValuations(context.Context, []market.Valuation) error
	StoreCollectorStatus(context.Context, market.CollectorStatus) error
}

type Config struct {
	WorkerToken, CollectorToken, AdminToken string
	DataMode                                string
	AllowedOrigins                          []string
	Logger                                  *slog.Logger
	Repository                              Repository
}
type Metrics struct {
	Listings         atomic.Uint64
	Transactions     atomic.Uint64
	Observations     atomic.Uint64
	Duplicates       atomic.Uint64
	WSConnections    atomic.Int64
	Flips            atomic.Uint64
	PurchaseAttempts atomic.Uint64
	PurchaseSuccess  atomic.Uint64
	ChatMessages     atomic.Uint64
	Rejected         atomic.Uint64
	DroppedFrames    atomic.Uint64
	SnapshotBytes    atomic.Uint64
	HTTPRequests     atomic.Uint64
	HTTPErrors       atomic.Uint64
	HTTPDurationNS   atomic.Uint64
	HTTPBuckets      [8]atomic.Uint64
}
type FlipEvent struct {
	ClientID  string    `json:"client_id"`
	Signature string    `json:"signature"`
	Price     int64     `json:"price"`
	Profit    int64     `json:"profit"`
	At        time.Time `json:"at"`
}
type PurchaseEvent struct {
	ClientID    string    `json:"client_id"`
	Fingerprint string    `json:"fingerprint"`
	Success     bool      `json:"success"`
	Reason      string    `json:"reason"`
	At          time.Time `json:"at"`
}
type ClientOpportunityFeed struct {
	Version       uint64              `json:"version"`
	GeneratedAt   time.Time           `json:"generated_at"`
	State         string              `json:"state"`
	Message       string              `json:"message,omitempty"`
	Opportunities []ClientOpportunity `json:"opportunities"`
}
type ClientOpportunity struct {
	Key             string    `json:"key"`
	AuthoritativeID string    `json:"authoritative_id,omitempty"`
	Fingerprint     string    `json:"fingerprint"`
	Seller          string    `json:"seller"`
	ItemID          string    `json:"item_id"`
	Quantity        int       `json:"quantity"`
	Price           int64     `json:"price"`
	ReferenceValue  int64     `json:"reference_value"`
	Profit          int64     `json:"profit"`
	MarginBPS       int       `json:"margin_bps"`
	ConfidenceBPS   int       `json:"confidence_bps"`
	Volume24h       int       `json:"volume_24h"`
	ExpiresAt       time.Time `json:"expires_at,omitempty"`
}
type Dashboard struct {
	DataMode        string                 `json:"data_mode"`
	CollectorStatus market.CollectorStatus `json:"collector_status"`
	GeneratedAt     time.Time              `json:"generated_at"`
	Metrics         map[string]any         `json:"metrics"`
	Listings        []market.Listing       `json:"listings"`
	Transactions    []market.Transaction   `json:"transactions"`
	Valuations      []market.Valuation     `json:"valuations"`
	Workers         []network.WorkerState  `json:"workers"`
	Assignments     []network.Assignment   `json:"assignments"`
	Flips           []FlipEvent            `json:"flips"`
	Purchases       []PurchaseEvent        `json:"purchases"`
	Chat            []network.ChatMessage  `json:"chat"`
	Sharding        network.ShardingResult `json:"sharding"`
}

type Server struct {
	cfg                Config
	engine             *market.Engine
	metrics            Metrics
	hub                *hub
	mu                 sync.RWMutex
	workers            map[string]network.WorkerState
	flips              []FlipEvent
	purchases          []PurchaseEvent
	chat               []network.ChatMessage
	assignments        []network.Assignment
	limits             map[string]*rateWindow
	lastLimitSweep     time.Time
	lastOfficialIngest atomic.Int64
	collectorStatus    market.CollectorStatus
	opportunityVersion uint64
	opportunityState   string
	opportunityCache   []ClientOpportunity
}
type rateWindow struct {
	start time.Time
	count int
}

func NewServer(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.WorkerToken == "" {
		cfg.WorkerToken = "local-worker-token"
	}
	if cfg.AdminToken == "" {
		cfg.AdminToken = "local-admin-token"
	}
	if cfg.CollectorToken == "" {
		cfg.CollectorToken = "local-collector-token"
	}
	if cfg.DataMode == "" {
		cfg.DataMode = "live"
	}
	s := &Server{cfg: cfg, engine: market.NewEngine(), workers: map[string]network.WorkerState{}, limits: map[string]*rateWindow{}}
	s.hub = newHub(&s.metrics, cfg.Logger)
	return s
}
func (s *Server) Engine() *market.Engine { return s.engine }
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /metrics", s.authorizeAdmin(s.prometheus))
	mux.HandleFunc("GET /api/v1/snapshot", s.authorizeAny(s.snapshot))
	mux.HandleFunc("GET /api/v1/client-snapshot", s.authorizeAny(s.clientSnapshot))
	mux.HandleFunc("GET /api/v1/opportunities", s.authorizeAny(s.clientOpportunities))
	mux.HandleFunc("GET /api/v1/dashboard", s.authorizeAdmin(s.dashboard))
	mux.HandleFunc("GET /api/v1/debug/valuation", s.authorizeAdmin(s.debugValuation))
	mux.HandleFunc("POST /api/v1/ingest/transactions", s.authorizeCollector(s.ingestTransactions))
	mux.HandleFunc("POST /api/v1/ingest/listings", s.authorizeCollector(s.ingestListings))
	mux.HandleFunc("POST /api/v1/ingest/status", s.authorizeCollector(s.ingestStatus))
	mux.HandleFunc("POST /api/v1/observations", s.authorize(s.observation))
	mux.HandleFunc("POST /api/v1/telemetry", s.authorize(s.telemetry))
	mux.HandleFunc("GET /ws", s.websocket)
	return s.securityHeaders(s.observeHTTP(mux))
}
func (s *Server) observeHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// WebSocket upgrading needs the original ResponseWriter capabilities.
		if r.URL.Path == "/ws" {
			next.ServeHTTP(w, r)
			return
		}
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		duration := time.Since(started)
		s.metrics.HTTPRequests.Add(1)
		s.metrics.HTTPDurationNS.Add(uint64(duration))
		if recorder.status >= 400 {
			s.metrics.HTTPErrors.Add(1)
		}
		for i, upper := range [...]time.Duration{time.Millisecond, 5 * time.Millisecond, 10 * time.Millisecond, 25 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond, time.Second} {
			if duration <= upper {
				s.metrics.HTTPBuckets[i].Add(1)
			}
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusRecorder) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Write(data []byte) (int, error) {
	w.wroteHeader = true
	return w.ResponseWriter.Write(data)
}
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		origin := r.Header.Get("Origin")
		if origin != "" && s.originAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) originAllowed(origin string) bool {
	if strings.HasPrefix(origin, "http://localhost:") || strings.HasPrefix(origin, "http://127.0.0.1:") {
		return true
	}
	for _, allowed := range s.cfg.AllowedOrigins {
		if subtle.ConstantTimeCompare([]byte(origin), []byte(allowed)) == 1 {
			return true
		}
	}
	return false
}
func (s *Server) authorize(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if len(token) != len(s.cfg.WorkerToken) || subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.WorkerToken)) != 1 {
			s.metrics.Rejected.Add(1)
			writeError(w, http.StatusUnauthorized, "invalid bearer token")
			return
		}
		if !s.allow(r, 240, time.Minute) {
			s.metrics.Rejected.Add(1)
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next(w, r)
	}
}
func (s *Server) authorizeAdmin(next http.HandlerFunc) http.HandlerFunc {
	return s.authorizeWithTokens(next, 600, s.cfg.AdminToken)
}

func (s *Server) authorizeCollector(next http.HandlerFunc) http.HandlerFunc {
	return s.authorizeWithTokens(next, 240, s.cfg.CollectorToken)
}

func (s *Server) authorizeAny(next http.HandlerFunc) http.HandlerFunc {
	return s.authorizeWithTokens(next, 600, s.cfg.WorkerToken, s.cfg.AdminToken)
}

func (s *Server) authorizeWithTokens(next http.HandlerFunc, limit int, expected ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		valid := false
		for _, candidate := range expected {
			if len(token) == len(candidate) && subtle.ConstantTimeCompare([]byte(token), []byte(candidate)) == 1 {
				valid = true
			}
		}
		if !valid {
			s.metrics.Rejected.Add(1)
			writeError(w, http.StatusUnauthorized, "invalid bearer token")
			return
		}
		if !s.allow(r, limit, time.Minute) {
			s.metrics.Rejected.Add(1)
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next(w, r)
	}
}
func (s *Server) allow(r *http.Request, limit int, period time.Duration) bool {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if host == "" {
		host = r.RemoteAddr
	}
	if clientID := r.Header.Get("X-Client-ID"); clientID != "" && len(clientID) <= 80 {
		host += ":" + clientID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.lastLimitSweep.IsZero() || now.Sub(s.lastLimitSweep) >= period {
		for key, window := range s.limits {
			if now.Sub(window.start) >= period {
				delete(s.limits, key)
			}
		}
		s.lastLimitSweep = now
	}
	win := s.limits[host]
	if win == nil && len(s.limits) >= 10_000 {
		return false
	}
	if win == nil || now.Sub(win.start) >= period {
		s.limits[host] = &rateWindow{start: now, count: 1}
		return true
	}
	if win.count >= limit {
		return false
	}
	win.count++
	return true
}
func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "snapshot_version": s.engine.Version(), "time": time.Now().UTC()})
}
func (s *Server) snapshot(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.Snapshot())
}
func (s *Server) clientSnapshot(w http.ResponseWriter, r *http.Request) {
	version := s.engine.Version()
	etag := fmt.Sprintf("\"%d\"", version)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	snapshot := s.engine.ClientSnapshot()
	if snapshot.Version != version {
		w.Header().Set("ETag", fmt.Sprintf("\"%d\"", snapshot.Version))
	}
	writeJSON(w, http.StatusOK, snapshot)
}
func (s *Server) clientOpportunities(w http.ResponseWriter, r *http.Request) {
	s.engine.SweepExpired(time.Now().UTC())
	version := s.engine.Version()
	state, message := "ready", ""
	lastIngest := s.lastOfficialIngest.Load()
	if s.cfg.DataMode == "live" && (lastIngest == 0 || time.Since(time.UnixMilli(lastIngest)) > 2*time.Minute) {
		state, message = "stale", "official auction data is older than two minutes"
	}
	etag := fmt.Sprintf("\"opportunities-%d-%s\"", version, state)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	opportunities := s.cachedClientOpportunities(version, state)
	writeJSON(w, http.StatusOK, ClientOpportunityFeed{
		Version: version, GeneratedAt: time.Now().UTC(), State: state, Message: message,
		Opportunities: opportunities,
	})
}

func (s *Server) cachedClientOpportunities(version uint64, state string) []ClientOpportunity {
	s.mu.RLock()
	if s.opportunityVersion == version && s.opportunityState == state {
		cached := append([]ClientOpportunity(nil), s.opportunityCache...)
		s.mu.RUnlock()
		return cached
	}
	s.mu.RUnlock()
	ranked := []market.Opportunity{}
	if state == "ready" {
		ranked = s.engine.Opportunities(market.Thresholds{
			MinProfit: 1_000_000, MinMarginBPS: 1_000, MinConfidenceBPS: 3_000,
			MaxPurchasePrice: 900_000_000, MinVolume24h: 1,
		}, 25)
	}
	opportunities := make([]ClientOpportunity, 0, len(ranked))
	for _, opportunity := range ranked {
		listing := opportunity.Listing
		key := listing.AuthoritativeID
		if key == "" {
			key = fmt.Sprintf("%s:%d", listing.Fingerprint, listing.TotalPrice)
		}
		opportunities = append(opportunities, ClientOpportunity{
			Key: key, AuthoritativeID: listing.AuthoritativeID, Fingerprint: listing.Fingerprint,
			Seller: listing.SellerName, ItemID: listing.Item.ID, Quantity: listing.Item.Quantity,
			Price: listing.TotalPrice, ReferenceValue: opportunity.Profit + listing.TotalPrice,
			Profit: opportunity.Profit, MarginBPS: opportunity.MarginBPS,
			ConfidenceBPS: opportunity.Valuation.ConfidenceBPS, Volume24h: opportunity.Valuation.Volume24h,
			ExpiresAt: listing.ExpiresAt,
		})
	}
	s.mu.Lock()
	if s.opportunityVersion <= version || s.opportunityState != state {
		s.opportunityVersion = version
		s.opportunityState = state
		s.opportunityCache = append([]ClientOpportunity(nil), opportunities...)
	}
	s.mu.Unlock()
	return opportunities
}
func (s *Server) ingestStatus(w http.ResponseWriter, r *http.Request) {
	var status market.CollectorStatus
	if err := decodeJSON(w, r, &status, 16<<10); err != nil {
		return
	}
	if status.State != "collecting" && status.State != "ready" && status.State != "error" {
		writeError(w, http.StatusBadRequest, "collector state must be collecting, ready, or error")
		return
	}
	if status.CycleStartedAt.IsZero() || status.CycleStartedAt.After(time.Now().UTC().Add(5*time.Minute)) || status.ListingsFetched < 0 || status.TransactionsFetched < 0 || status.LastAPILatencyMS < 0 || status.CycleDurationMS < 0 {
		writeError(w, http.StatusBadRequest, "collector status contains invalid counters or timestamps")
		return
	}
	if len(status.Message) > 500 {
		status.Message = status.Message[:500]
	}
	if s.cfg.Repository != nil {
		if err := s.cfg.Repository.StoreCollectorStatus(r.Context(), status); err != nil {
			s.cfg.Logger.Error("persist collector status", "error", err)
		}
	}
	s.mu.Lock()
	s.collectorStatus = status
	s.mu.Unlock()
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true})
}

func (s *Server) debugValuation(w http.ResponseWriter, r *http.Request) {
	signature := strings.TrimSpace(r.URL.Query().Get("signature"))
	if signature == "" || len(signature) > 2_048 {
		writeError(w, http.StatusBadRequest, "signature query parameter is required")
		return
	}
	writeJSON(w, http.StatusOK, s.engine.Explain(signature))
}
func (s *Server) ingestTransactions(w http.ResponseWriter, r *http.Request) {
	var ts []market.Transaction
	if err := decodeJSON(w, r, &ts, 1<<20); err != nil {
		return
	}
	if len(ts) > 1000 {
		writeError(w, 413, "maximum 1000 transactions per batch")
		return
	}
	for _, transaction := range ts {
		if !s.acceptsBatchSource(transaction.Source) {
			s.metrics.Rejected.Add(1)
			writeError(w, http.StatusConflict, "simulated data is disabled in live mode")
			return
		}
	}
	for index, transaction := range ts {
		if err := validateTransaction(transaction, time.Now().UTC()); err != nil {
			s.metrics.Rejected.Add(1)
			writeError(w, http.StatusBadRequest, fmt.Sprintf("transaction %d: %s", index, err))
			return
		}
	}
	for i := range ts {
		ts[i] = market.NormalizeTransaction(ts[i])
	}
	if s.cfg.Repository != nil {
		if err := s.cfg.Repository.StoreTransactions(r.Context(), ts); err != nil {
			s.cfg.Logger.Error("persist transactions", "error", err)
			writeError(w, http.StatusServiceUnavailable, "database unavailable")
			return
		}
	}
	added := s.engine.AddTransactions(ts)
	if s.cfg.Repository != nil {
		changed := map[string]struct{}{}
		for _, transaction := range ts {
			changed[transaction.Signature.Exact] = struct{}{}
			changed[transaction.Signature.Base] = struct{}{}
		}
		valuations := make([]market.Valuation, 0, len(changed))
		for signature := range changed {
			if valuation, ok := s.engine.Valuation(signature); ok {
				valuations = append(valuations, valuation)
			}
		}
		if err := s.cfg.Repository.StoreValuations(r.Context(), valuations); err != nil {
			s.cfg.Logger.Error("persist valuations", "error", err)
			writeError(w, http.StatusServiceUnavailable, "database unavailable")
			return
		}
	}
	if len(ts) > 0 && ts[0].Source == market.SourceDonutAPI {
		s.lastOfficialIngest.Store(time.Now().UTC().UnixMilli())
	}
	s.metrics.Transactions.Add(uint64(added))
	s.broadcastSnapshot()
	writeJSON(w, 202, map[string]any{"accepted": added, "snapshot_version": s.engine.Snapshot().Version})
}
func (s *Server) ingestListings(w http.ResponseWriter, r *http.Request) {
	var ls []market.Listing
	if err := decodeJSON(w, r, &ls, 1<<20); err != nil {
		return
	}
	if len(ls) > 1000 {
		writeError(w, 413, "maximum 1000 listings per batch")
		return
	}
	for _, listing := range ls {
		if !s.acceptsBatchSource(listing.Source) {
			s.metrics.Rejected.Add(1)
			writeError(w, http.StatusConflict, "simulated data is disabled in live mode")
			return
		}
	}
	for index, listing := range ls {
		if err := validateListing(listing, time.Now().UTC()); err != nil {
			s.metrics.Rejected.Add(1)
			writeError(w, http.StatusBadRequest, fmt.Sprintf("listing %d: %s", index, err))
			return
		}
	}
	for i := range ls {
		ls[i] = market.NormalizeListing(ls[i])
	}
	if s.cfg.Repository != nil {
		if err := s.cfg.Repository.UpsertListings(r.Context(), ls); err != nil {
			s.cfg.Logger.Error("persist listings", "error", err)
			writeError(w, http.StatusServiceUnavailable, "database unavailable")
			return
		}
	}
	normalizedListings, dedup := s.engine.ObserveBatch(ls)
	s.metrics.Duplicates.Add(uint64(dedup))
	s.metrics.Listings.Add(uint64(len(normalizedListings) - dedup))
	updatedSignatures := map[string]struct{}{}
	for _, normalized := range normalizedListings {
		s.hub.broadcast(network.P1, network.MsgListing, normalized)
		updatedSignatures[normalized.Signature.Exact] = struct{}{}
		updatedSignatures[normalized.Signature.Base] = struct{}{}
	}
	for signature := range updatedSignatures {
		s.broadcastPriceUpdate(signature)
	}
	if len(ls) > 0 && ls[0].Source == market.SourceDonutAPI {
		s.lastOfficialIngest.Store(time.Now().UTC().UnixMilli())
	}
	writeJSON(w, 202, map[string]any{"accepted": len(ls), "deduplicated": dedup})
}
func (s *Server) observation(w http.ResponseWriter, r *http.Request) {
	var l market.Listing
	if err := decodeJSON(w, r, &l, network.MaxFrameSize); err != nil {
		return
	}
	if !s.acceptsSource(l.Source) {
		s.metrics.Rejected.Add(1)
		writeError(w, http.StatusConflict, "simulated data is disabled in live mode")
		return
	}
	if err := validateListing(l, time.Now().UTC()); err != nil {
		s.metrics.Rejected.Add(1)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.metrics.Observations.Add(1)
	l = market.NormalizeListing(l)
	if s.cfg.Repository != nil {
		if err := s.cfg.Repository.UpsertListings(r.Context(), []market.Listing{l}); err != nil {
			s.cfg.Logger.Error("persist observation", "error", err)
			writeError(w, http.StatusServiceUnavailable, "database unavailable")
			return
		}
	}
	normalized, duplicate := s.engine.Observe(l)
	if duplicate {
		s.metrics.Duplicates.Add(1)
	} else {
		s.metrics.Listings.Add(1)
	}
	s.hub.broadcast(network.P1, network.MsgListingObserved, normalized)
	s.broadcastValuationFamily(normalized.Signature)
	writeJSON(w, 202, map[string]any{"fingerprint": normalized.Fingerprint, "duplicate": duplicate})
}
func (s *Server) telemetry(w http.ResponseWriter, r *http.Request) {
	var event network.TelemetryEvent
	if err := decodeJSON(w, r, &event, network.MaxFrameSize); err != nil {
		return
	}
	s.handleTelemetry(event)
	w.WriteHeader(http.StatusAccepted)
}
func (s *Server) handleTelemetry(event network.TelemetryEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch event.Kind {
	case "FLIP_DETECTED":
		s.metrics.Flips.Add(1)
		s.flips = appendBounded(s.flips, FlipEvent{event.ClientID, event.Signature, event.Price, parseInt64(event.Metadata["profit"]), time.Now().UTC()}, 100)
	case "PURCHASE_ATTEMPT":
		s.metrics.PurchaseAttempts.Add(1)
	case "LISTING_GONE":
		if s.engine.RemoveListing(event.Fingerprint) {
			base := strings.SplitN(event.Signature, "|", 2)[0]
			s.broadcastValuationFamily(market.Signature{Exact: event.Signature, Base: base})
		}
	case "PURCHASE_SUCCESS", "PURCHASE_FAILED":
		if event.Success {
			s.metrics.PurchaseSuccess.Add(1)
		}
		s.purchases = appendBounded(s.purchases, PurchaseEvent{event.ClientID, event.Fingerprint, event.Success, event.Metadata["reason"], time.Now().UTC()}, 100)
	}
}
func (s *Server) dashboard(w http.ResponseWriter, _ *http.Request) {
	s.engine.SweepExpired(time.Now().UTC())
	s.mu.RLock()
	workers := values(s.workers)
	flips := append([]FlipEvent(nil), s.flips...)
	purchases := append([]PurchaseEvent(nil), s.purchases...)
	chat := append([]network.ChatMessage(nil), s.chat...)
	assignments := append([]network.Assignment(nil), s.assignments...)
	collectorStatus := s.collectorStatus
	s.mu.RUnlock()
	if collectorStatus.State == "ready" && !collectorStatus.LastSuccessAt.IsZero() && time.Since(collectorStatus.LastSuccessAt) > 2*time.Minute {
		collectorStatus.State = "stale"
		collectorStatus.Message = "collector has not completed a cycle in over two minutes"
	}
	snap := s.engine.Snapshot()
	vals := make([]market.Valuation, 0, len(snap.Valuations))
	for _, v := range snap.Valuations {
		vals = append(vals, v)
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i].ConfidenceBPS > vals[j].ConfidenceBPS })
	attempts := s.metrics.PurchaseAttempts.Load()
	successRate := float64(0)
	if attempts > 0 {
		successRate = float64(s.metrics.PurchaseSuccess.Load()) / float64(attempts)
	}
	httpRequests := s.metrics.HTTPRequests.Load()
	averageHTTPMS := float64(0)
	if httpRequests > 0 {
		averageHTTPMS = float64(s.metrics.HTTPDurationNS.Load()) / float64(httpRequests) / float64(time.Millisecond)
	}
	writeJSON(w, 200, Dashboard{
		DataMode: s.cfg.DataMode, CollectorStatus: collectorStatus, GeneratedAt: time.Now().UTC(),
		Metrics:  map[string]any{"listings": s.metrics.Listings.Load(), "transactions": s.metrics.Transactions.Load(), "observations": s.metrics.Observations.Load(), "duplicates": s.metrics.Duplicates.Load(), "websocket_connections": s.metrics.WSConnections.Load(), "flips": s.metrics.Flips.Load(), "purchase_attempts": attempts, "purchase_success_rate": successRate, "snapshot_version": snap.Version, "snapshot_bytes": s.metrics.SnapshotBytes.Load(), "dropped_frames": s.metrics.DroppedFrames.Load(), "http_requests": httpRequests, "http_errors": s.metrics.HTTPErrors.Load(), "http_average_ms": averageHTTPMS, "official_ingest_unix_ms": s.lastOfficialIngest.Load()},
		Listings: s.engine.Listings(50), Transactions: s.engine.Transactions(50), Valuations: vals,
		Workers: workers, Assignments: assignments, Flips: flips, Purchases: purchases, Chat: chat,
		Sharding: network.CompareSharding(max(1, len(workers)), max(1, len(vals)), 20),
	})
}
func (s *Server) prometheus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "dn_listings_total %d\ndn_transactions_total %d\ndn_observations_total %d\ndn_deduplicates_total %d\ndn_websocket_connections %d\ndn_flips_total %d\ndn_purchase_attempts_total %d\ndn_purchase_success_total %d\ndn_rejected_messages_total %d\ndn_dropped_frames_total %d\n", s.metrics.Listings.Load(), s.metrics.Transactions.Load(), s.metrics.Observations.Load(), s.metrics.Duplicates.Load(), s.metrics.WSConnections.Load(), s.metrics.Flips.Load(), s.metrics.PurchaseAttempts.Load(), s.metrics.PurchaseSuccess.Load(), s.metrics.Rejected.Load(), s.metrics.DroppedFrames.Load())
	fmt.Fprintf(w, "dn_http_requests_total %d\ndn_http_errors_total %d\ndn_http_request_duration_seconds_sum %.9f\ndn_http_request_duration_seconds_count %d\n", s.metrics.HTTPRequests.Load(), s.metrics.HTTPErrors.Load(), float64(s.metrics.HTTPDurationNS.Load())/float64(time.Second), s.metrics.HTTPRequests.Load())
	for i, upper := range [...]string{"0.001", "0.005", "0.01", "0.025", "0.05", "0.1", "0.25", "1"} {
		fmt.Fprintf(w, "dn_http_request_duration_seconds_bucket{le=\"%s\"} %d\n", upper, s.metrics.HTTPBuckets[i].Load())
	}
	fmt.Fprintf(w, "dn_http_request_duration_seconds_bucket{le=\"+Inf\"} %d\n", s.metrics.HTTPRequests.Load())
}
func (s *Server) broadcastSnapshot() {
	snap := s.engine.Snapshot()
	frames, err := encodeSnapshotFrames(snap)
	if err != nil {
		s.cfg.Logger.Error("encode valuation snapshot", "error", err)
		s.metrics.DroppedFrames.Add(1)
		return
	}
	total := 0
	for _, frame := range frames {
		total += len(frame)
		s.hub.broadcastEncoded(network.P1, frame)
	}
	s.metrics.SnapshotBytes.Store(uint64(total))
}

func encodeSnapshotFrames(snapshot market.Snapshot) ([][]byte, error) {
	if encoded, err := network.Encode(network.P1, network.MsgSnapshot, snapshot); err == nil {
		return [][]byte{encoded}, nil
	}
	keys := make([]string, 0, len(snapshot.Valuations))
	for signature := range snapshot.Valuations {
		keys = append(keys, signature)
	}
	sort.Strings(keys)
	const entriesPerChunk = 32
	count := (len(keys) + entriesPerChunk - 1) / entriesPerChunk
	frames := make([][]byte, 0, count)
	for index, start := 0, 0; start < len(keys); index, start = index+1, start+entriesPerChunk {
		end := min(start+entriesPerChunk, len(keys))
		valuations := make(map[string]market.Valuation, end-start)
		for _, signature := range keys[start:end] {
			valuations[signature] = snapshot.Valuations[signature]
		}
		encoded, err := network.Encode(network.P1, network.MsgSnapshotChunk, market.SnapshotChunk{
			Version: snapshot.Version, GeneratedAt: snapshot.GeneratedAt, Index: index, Count: count, Valuations: valuations,
		})
		if err != nil {
			return nil, fmt.Errorf("snapshot chunk %d: %w", index, err)
		}
		frames = append(frames, encoded)
	}
	return frames, nil
}

func (s *Server) broadcastPriceUpdate(signature string) {
	update, ok := s.engine.PriceUpdate(signature)
	if ok {
		s.hub.broadcast(network.P1, network.MsgPriceUpdate, update)
		return
	}
	s.hub.broadcast(network.P0, network.MsgPriceInvalidation, map[string]any{"version": update.Version, "generated_at": update.GeneratedAt, "signature": signature})
}

func (s *Server) broadcastValuationFamily(signature market.Signature) {
	s.broadcastPriceUpdate(signature.Exact)
	if signature.Base != signature.Exact {
		s.broadcastPriceUpdate(signature.Base)
	}
}

func (s *Server) websocket(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		// Browser WebSocket constructors cannot set an Authorization header.
		token = r.URL.Query().Get("token")
	}
	if len(token) != len(s.cfg.WorkerToken) || subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.WorkerToken)) != 1 {
		s.metrics.Rejected.Add(1)
		writeError(w, 401, "invalid websocket token")
		return
	}
	clientMode := r.Header.Get("X-Data-Mode")
	if clientMode == "" {
		clientMode = "live"
	}
	if clientMode != s.cfg.DataMode {
		s.metrics.Rejected.Add(1)
		writeError(w, http.StatusConflict, "client data mode does not match backend")
		return
	}
	upgrader := websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096, CheckOrigin: func(req *http.Request) bool {
		origin := req.Header.Get("Origin")
		return origin == "" || s.originAllowed(origin)
	}}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := s.hub.add(conn)
	defer func() {
		workerID := client.worker()
		s.hub.remove(client)
		if workerID != "" && !s.hub.hasWorker(workerID) {
			s.mu.Lock()
			delete(s.workers, workerID)
			s.reassignLocked()
			s.mu.Unlock()
		}
	}()
	conn.SetReadLimit(network.MaxFrameSize + 7)
	_ = conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(45 * time.Second)) })
	snapshotFrames, err := encodeSnapshotFrames(s.engine.Snapshot())
	if err != nil {
		return
	}
	for _, snapshotFrame := range snapshotFrames {
		if err := client.queue.Push(network.P0, snapshotFrame); err != nil {
			return
		}
	}
	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if kind != websocket.BinaryMessage {
			s.metrics.Rejected.Add(1)
			continue
		}
		frame, err := network.Decode(data)
		if err != nil {
			s.metrics.Rejected.Add(1)
			continue
		}
		s.handleFrame(client, frame)
	}
}
func (s *Server) handleFrame(c *wsClient, f network.Frame) {
	if !c.allow(f.Type, time.Now()) {
		s.metrics.Rejected.Add(1)
		return
	}
	switch f.Type {
	case network.MsgWorkerHeartbeat:
		var w network.WorkerState
		if json.Unmarshal(f.Payload, &w) == nil && w.WorkerID != "" {
			c.setWorker(w.WorkerID)
			w.Online = true
			w.LastHeartbeat = time.Now().UTC()
			s.mu.Lock()
			s.workers[w.WorkerID] = w
			s.reassignLocked()
			s.mu.Unlock()
		}
	case network.MsgListingObserved:
		var l market.Listing
		if json.Unmarshal(f.Payload, &l) == nil {
			if !s.acceptsSource(l.Source) {
				s.metrics.Rejected.Add(1)
				return
			}
			if err := validateListing(l, time.Now().UTC()); err != nil {
				s.metrics.Rejected.Add(1)
				return
			}
			s.metrics.Observations.Add(1)
			l = market.NormalizeListing(l)
			if s.cfg.Repository != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				err := s.cfg.Repository.UpsertListings(ctx, []market.Listing{l})
				cancel()
				if err != nil {
					s.cfg.Logger.Error("persist websocket observation", "error", err)
					s.metrics.Rejected.Add(1)
					return
				}
			}
			normalized, dup := s.engine.Observe(l)
			if dup {
				s.metrics.Duplicates.Add(1)
			} else {
				s.metrics.Listings.Add(1)
			}
			s.hub.broadcast(network.P1, network.MsgListingObserved, normalized)
			s.broadcastValuationFamily(normalized.Signature)
		}
	case network.MsgFlipDetected, network.MsgPurchaseResult:
		var e network.TelemetryEvent
		if json.Unmarshal(f.Payload, &e) == nil {
			s.handleTelemetry(e)
		}
	case network.MsgChat:
		var m network.ChatMessage
		if json.Unmarshal(f.Payload, &m) == nil && len(m.Text) <= 500 && (m.Channel == "global" || m.Channel == "flips" || m.Channel == "help" || m.Channel == "dm") {
			m.SentAt = time.Now().UnixMilli()
			s.mu.Lock()
			s.chat = appendBounded(s.chat, m, 100)
			s.mu.Unlock()
			s.metrics.ChatMessages.Add(1)
			s.hub.broadcast(network.P2, network.MsgChat, m)
		}
	}
}

func (s *Server) acceptsSource(source market.Source) bool {
	if s.cfg.DataMode == "simulation" {
		return source == market.SourceSimulator
	}
	return source == market.SourceClient || source == market.SourceDonutAPI
}

func (s *Server) acceptsBatchSource(source market.Source) bool {
	if s.cfg.DataMode == "simulation" {
		return source == market.SourceSimulator
	}
	return source == market.SourceDonutAPI
}
func (s *Server) reassignLocked() {
	targets := []network.SearchTarget{{ID: "elytra", Query: "elytra", Category: "mobility", ExpectedProfit: 12_000_000, ListingsPerMinute: 8, CompetitionBPS: 6500, MinBalance: 200_000_000}, {ID: "netherite", Query: "netherite", Category: "equipment", ExpectedProfit: 7_000_000, ListingsPerMinute: 24, CompetitionBPS: 5000, MinBalance: 50_000_000}, {ID: "spawner", Query: "spawner", Category: "utility", ExpectedProfit: 4_000_000, ListingsPerMinute: 14, CompetitionBPS: 3800, MinBalance: 15_000_000}, {ID: "mace", Query: "mace", Category: "equipment", ExpectedProfit: 18_000_000, ListingsPerMinute: 3, CompetitionBPS: 7200, MinBalance: 300_000_000}}
	s.assignments = network.Schedule(values(s.workers), targets, time.Now().UTC())
	for _, a := range s.assignments {
		s.hub.sendToWorker(a.WorkerID, network.P0, network.MsgAssignment, a)
	}
}

type wsClient struct {
	conn        *websocket.Conn
	queue       *network.PriorityQueue
	done        chan struct{}
	rateMu      sync.Mutex
	rateStarted time.Time
	rateCount   int
	chatCount   int
	identityMu  sync.RWMutex
	workerID    string
}
type hub struct {
	mu      sync.RWMutex
	clients map[*wsClient]struct{}
	metrics *Metrics
	logger  *slog.Logger
}

func newHub(m *Metrics, l *slog.Logger) *hub {
	return &hub{clients: map[*wsClient]struct{}{}, metrics: m, logger: l}
}
func (h *hub) add(conn *websocket.Conn) *wsClient {
	c := &wsClient{conn: conn, queue: network.NewPriorityQueue(256), done: make(chan struct{})}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	h.metrics.WSConnections.Add(1)
	go c.writeLoop()
	return c
}
func (h *hub) remove(c *wsClient) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		h.metrics.WSConnections.Add(-1)
		close(c.done)
		c.queue.Close()
		_ = c.conn.Close()
	}
	h.mu.Unlock()
}
func (h *hub) broadcast(p network.Priority, t network.MessageType, v any) {
	b, err := network.Encode(p, t, v)
	if err == nil {
		h.broadcastEncoded(p, b)
	}
}
func (h *hub) broadcastEncoded(p network.Priority, b []byte) {
	h.mu.RLock()
	overloaded := make([]*wsClient, 0)
	for c := range h.clients {
		if err := c.queue.Push(p, b); err != nil {
			h.metrics.DroppedFrames.Add(1)
			if p != network.P2 {
				overloaded = append(overloaded, c)
			}
		}
	}
	h.mu.RUnlock()
	for _, c := range overloaded {
		h.remove(c)
	}
}
func (h *hub) sendToWorker(workerID string, p network.Priority, t network.MessageType, v any) {
	b, err := network.Encode(p, t, v)
	if err != nil {
		return
	}
	h.mu.RLock()
	var overloaded []*wsClient
	for c := range h.clients {
		if c.worker() == workerID {
			if err := c.queue.Push(p, b); err != nil {
				h.metrics.DroppedFrames.Add(1)
				overloaded = append(overloaded, c)
			}
		}
	}
	h.mu.RUnlock()
	for _, c := range overloaded {
		h.remove(c)
	}
}
func (h *hub) hasWorker(workerID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		if c.worker() == workerID {
			return true
		}
	}
	return false
}
func (c *wsClient) writeLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			_ = c.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second))
		case <-c.queue.Wake():
			for {
				b, _, ok := c.queue.TryPop()
				if !ok {
					break
				}
				_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if err := c.conn.WriteMessage(websocket.BinaryMessage, b); err != nil {
					_ = c.conn.Close()
					return
				}
			}
		}
	}
}

func (c *wsClient) allow(typ network.MessageType, now time.Time) bool {
	c.rateMu.Lock()
	defer c.rateMu.Unlock()
	if c.rateStarted.IsZero() || now.Sub(c.rateStarted) >= 10*time.Second {
		c.rateStarted, c.rateCount, c.chatCount = now, 0, 0
	}
	c.rateCount++
	if c.rateCount > 2_000 {
		return false
	}
	if typ == network.MsgChat {
		c.chatCount++
		return c.chatCount <= 20
	}
	return true
}

func (c *wsClient) setWorker(workerID string) {
	c.identityMu.Lock()
	c.workerID = workerID
	c.identityMu.Unlock()
}

func (c *wsClient) worker() string {
	c.identityMu.RLock()
	defer c.identityMu.RUnlock()
	return c.workerID
}

func validateTransaction(transaction market.Transaction, now time.Time) error {
	if transaction.TotalPrice <= 0 {
		return fmt.Errorf("total_price must be positive")
	}
	if transaction.SoldAt.IsZero() || transaction.SoldAt.Before(now.Add(-transactionRetentionLimit)) || transaction.SoldAt.After(now.Add(5*time.Minute)) {
		return fmt.Errorf("sold_at is outside the accepted history window")
	}
	if len(transaction.Fingerprint) > 256 || len(transaction.SellerUUID) > 64 || len(transaction.SellerName) > 64 {
		return fmt.Errorf("identity field is too long")
	}
	return validateItem(transaction.Item, 0)
}

const transactionRetentionLimit = 31 * 24 * time.Hour

func validateListing(listing market.Listing, now time.Time) error {
	if listing.TotalPrice <= 0 {
		return fmt.Errorf("total_price must be positive")
	}
	if len(listing.Fingerprint) > 256 || len(listing.AuthoritativeID) > 256 || len(listing.SellerUUID) > 64 || len(listing.SellerName) > 64 || len(listing.SearchContext) > 256 {
		return fmt.Errorf("listing field is too long")
	}
	if listing.Page < 0 || listing.Page > 1_000_000 {
		return fmt.Errorf("page is outside its valid range")
	}
	if !listing.ExpiresAt.IsZero() && (listing.ExpiresAt.Before(now.Add(-5*time.Minute)) || listing.ExpiresAt.After(now.Add(31*24*time.Hour))) {
		return fmt.Errorf("expires_at is outside its valid range")
	}
	return validateItem(listing.Item, 0)
}

func validateItem(item market.Item, depth int) error {
	if depth > 4 {
		return fmt.Errorf("container nesting exceeds four levels")
	}
	if strings.TrimSpace(item.ID) == "" || len(item.ID) > 128 {
		return fmt.Errorf("item id is missing or too long")
	}
	if item.Quantity < 1 || item.Quantity > 1_728 {
		return fmt.Errorf("item quantity is outside 1..1728")
	}
	if len(item.DisplayName) > 256 || len(item.Lore) > 64 || len(item.Enchantments) > 128 || len(item.Components) > 128 || len(item.Contents) > 128 {
		return fmt.Errorf("item metadata exceeds its size limit")
	}
	for _, line := range item.Lore {
		if len(line) > 1_024 {
			return fmt.Errorf("item lore line is too long")
		}
	}
	for enchantment, level := range item.Enchantments {
		if len(enchantment) > 128 || level < 0 || level > 255 {
			return fmt.Errorf("invalid enchantment metadata")
		}
	}
	for key, value := range item.Components {
		if len(key) > 128 || len(value) > 1_024 {
			return fmt.Errorf("invalid component metadata")
		}
	}
	for _, child := range item.Contents {
		if err := validateItem(child, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, 400, "invalid JSON: "+err.Error())
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeError(w, 400, "request must contain one JSON value")
		return fmt.Errorf("trailing json")
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"status": status, "error": message})
}
func appendBounded[T any](s []T, v T, n int) []T {
	s = append(s, v)
	if len(s) > n {
		s = append([]T(nil), s[len(s)-n:]...)
	}
	return s
}
func values[K comparable, V any](m map[K]V) []V {
	out := make([]V, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
func parseInt64(s string) int64 { var v int64; fmt.Sscan(s, &v); return v }
