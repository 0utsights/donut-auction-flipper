package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"donut-network/internal/orders"
)

const maxObserverBody = 1 << 20

var (
	observerIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	hashPattern       = regexp.MustCompile(`^[a-f0-9]{64}$`)
	itemIDPattern     = regexp.MustCompile(`^[a-z0-9_.-]+:[a-z0-9_./-]+$`)
	ownerPattern      = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)
	identifierPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

func (s *Server) observerRegister(w http.ResponseWriter, r *http.Request) {
	var value orders.ObserverRegistration
	if !decodeRequest(w, r, &value) {
		return
	}
	if !observerIDPattern.MatchString(value.ObserverID) || !shortText(value.ParserVersion, 64) || !shortText(value.ProxyLabel, 64) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid observer registration"})
		return
	}
	observer, err := s.orders.Register(r.Context(), value)
	if err != nil {
		s.orderError(w, "register observer", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"observer": observer, "schema_version": orders.SchemaVersion})
}

func (s *Server) observerHeartbeat(w http.ResponseWriter, r *http.Request) {
	var value orders.Heartbeat
	if !decodeRequest(w, r, &value) {
		return
	}
	if !observerIDPattern.MatchString(value.ObserverID) || !allowedObserverState(value.State) || value.Page < 0 || value.Page > 100_000 || value.LatencyMS < 0 || value.LatencyMS > 300_000 || value.ReconnectCount < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid observer heartbeat"})
		return
	}
	if value.TaskID != "" && (!identifierPattern.MatchString(value.TaskID) || !identifierPattern.MatchString(value.LeaseToken)) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid task id"})
		return
	}
	if err := s.orders.Heartbeat(r.Context(), value); err != nil {
		s.orderError(w, "observer heartbeat", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) observerTasks(w http.ResponseWriter, r *http.Request) {
	observerID := strings.TrimSpace(r.URL.Query().Get("observer_id"))
	if !observerIDPattern.MatchString(observerID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid observer_id"})
		return
	}
	wait := 20 * time.Second
	if raw := strings.TrimSpace(r.URL.Query().Get("wait_ms")); raw != "" {
		milliseconds, err := strconv.Atoi(raw)
		if err != nil || milliseconds < 0 || milliseconds > 25_000 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "wait_ms must be between 0 and 25000"})
			return
		}
		wait = time.Duration(milliseconds) * time.Millisecond
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		task, err := s.orders.LeaseTask(r.Context(), observerID)
		if err != nil {
			s.orderError(w, "lease observer task", err)
			return
		}
		if task != nil {
			writeJSON(w, http.StatusOK, map[string]any{"task": task})
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			w.WriteHeader(http.StatusNoContent)
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) observerOrderScans(w http.ResponseWriter, r *http.Request) {
	var value orders.ScanBatch
	if !decodeRequest(w, r, &value) {
		return
	}
	if err := validateScan(value, s.now()); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	inserted, err := s.orders.SaveScan(r.Context(), value)
	if err != nil {
		s.orderError(w, "save order scan", err)
		return
	}
	if inserted {
		s.refreshOrderCandidates(r.Context())
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "duplicate": !inserted})
}

func (s *Server) observerTaskResult(w http.ResponseWriter, r *http.Request) {
	var value orders.TaskResult
	if !decodeRequest(w, r, &value) {
		return
	}
	if !observerIDPattern.MatchString(value.ObserverID) || !identifierPattern.MatchString(value.TaskID) || !identifierPattern.MatchString(value.LeaseToken) || !oneOf(value.Status, "complete", "retry", "failed") || !optionalText(value.Message, 200) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid task result"})
		return
	}
	if err := s.orders.CompleteTask(r.Context(), value); err != nil {
		s.orderError(w, "complete task", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) candidateFeed(w http.ResponseWriter, r *http.Request) {
	feed := s.orders.CandidateFeed()
	etag := `"orders-` + uintString(feed.Version) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, http.StatusOK, feed)
}

func (s *Server) addWatch(w http.ResponseWriter, r *http.Request) {
	var value orders.WatchRequest
	if !decodeRequest(w, r, &value) {
		return
	}
	value.Signature = strings.TrimSpace(value.Signature)
	if !safeSignature(value.Signature) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid signature"})
		return
	}
	found := false
	for _, candidate := range s.orders.CandidateFeed().Candidates {
		if candidate.Signature == value.Signature {
			found = true
			break
		}
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "candidate signature is not available"})
		return
	}
	watch, err := s.orders.AddWatch(r.Context(), value.Signature)
	if err != nil {
		s.orderError(w, "add focused watch", err)
		return
	}
	writeJSON(w, http.StatusCreated, watch)
}

func (s *Server) deleteWatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !identifierPattern.MatchString(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid watch id"})
		return
	}
	if err := s.orders.DeleteWatch(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "watch not found"})
			return
		}
		s.orderError(w, "delete focused watch", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) clientDiagnostics(w http.ResponseWriter, r *http.Request) {
	var values []orders.Diagnostic
	if !decodeRequest(w, r, &values) {
		return
	}
	if len(values) == 0 || len(values) > 50 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "diagnostic batch must contain 1-50 events"})
		return
	}
	for _, value := range values {
		if err := validateDiagnostic(value, s.now()); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.orders.SaveDiagnostic(r.Context(), value); err != nil {
			if errors.Is(err, orders.ErrDiagnosticRateLimit) {
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "diagnostic rate limit exceeded"})
				return
			}
			s.orderError(w, "save client diagnostic", err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) refreshOrderCandidates(ctx context.Context) {
	engine := s.engine.Load()
	if engine == nil {
		return
	}
	if err := s.orders.Refresh(ctx, engine); err != nil {
		s.logger.Warn("refresh order candidates", "error", err)
	}
}

func (s *Server) orderError(w http.ResponseWriter, operation string, err error) {
	s.logger.Error(operation, "error", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": operation + " failed"})
}

func decodeRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxObserverBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON request"})
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request must contain one JSON value"})
		return false
	}
	return true
}

func validateScan(value orders.ScanBatch, now time.Time) error {
	if value.SchemaVersion != orders.SchemaVersion {
		return errors.New("unsupported order schema version")
	}
	if !observerIDPattern.MatchString(value.ObserverID) || !identifierPattern.MatchString(value.SessionID) || !hashPattern.MatchString(value.ContentHash) {
		return errors.New("invalid scan identity")
	}
	if value.TaskID != "" && (!identifierPattern.MatchString(value.TaskID) || !identifierPattern.MatchString(value.LeaseToken)) {
		return errors.New("invalid scan task")
	}
	if !shortText(value.ScreenTitle, 128) || value.Page < 0 || value.Page > 100_000 || value.ObservedAt.Before(now.Add(-24*time.Hour)) || value.ObservedAt.After(now.Add(5*time.Minute)) {
		return errors.New("invalid scan metadata")
	}
	if len(value.Orders) > 256 || !optionalText(value.SchemaReason, 200) {
		return errors.New("invalid scan contents")
	}
	for _, order := range value.Orders {
		if !safeOrder(order) {
			return errors.New("invalid order observation")
		}
	}
	return nil
}

func safeOrder(value orders.OrderObservation) bool {
	return identifierPattern.MatchString(value.OrderKey) && itemIDPattern.MatchString(value.ItemID) && safeSignature(value.Signature) &&
		optionalText(value.DisplayName, 128) && value.Quantity >= 1 && value.Quantity <= 1728 && value.MaxStackSize >= 1 && value.MaxStackSize <= 99 &&
		value.UnitReward > 0 && value.UnitReward <= 9_000_000_000_000_000_000 && value.RequestedQuantity >= value.RemainingQuantity &&
		value.RequestedQuantity > 0 && value.RequestedQuantity <= 1_000_000_000_000 && value.RemainingQuantity >= 0 &&
		(value.Owner == "" || ownerPattern.MatchString(value.Owner)) && value.PricePosition >= 0 && value.PricePosition <= 1_000_000 &&
		value.Slot >= 0 && value.Slot <= 1_000 && hashPattern.MatchString(value.RawFieldHash)
}

func validateDiagnostic(value orders.Diagnostic, now time.Time) error {
	if !identifierPattern.MatchString(value.InstallID) || !shortText(value.Version, 64) || !oneOf(value.Event, "startup", "connection", "latency", "decision", "error", "outcome", "shutdown") ||
		!optionalText(value.Code, 64) || value.Duration < 0 || value.Duration > 3_600_000 || len(value.Fields) > 8 {
		return errors.New("invalid diagnostic event")
	}
	allowed := map[string]bool{"state": true, "candidate_state": true, "exception_class": true, "endpoint": true, "http_status": true, "model_version": true, "reason_code": true, "route": true}
	for key, field := range value.Fields {
		if !allowed[key] || !optionalText(field, 128) || strings.Contains(field, "://") || strings.ContainsAny(field, "\r\n") {
			return errors.New("diagnostic contains a forbidden field")
		}
	}
	if !value.CreatedAt.IsZero() && (value.CreatedAt.Before(now.Add(-24*time.Hour)) || value.CreatedAt.After(now.Add(5*time.Minute))) {
		return errors.New("invalid diagnostic timestamp")
	}
	return nil
}

func safeSignature(value string) bool {
	if value == "" || len(value) > 2048 || strings.ContainsAny(value, "\r\n\x00") {
		return false
	}
	for _, character := range value {
		if character < 32 || character > 126 {
			return false
		}
	}
	return true
}
func shortText(value string, limit int) bool { return value != "" && optionalText(value, limit) }
func optionalText(value string, limit int) bool {
	return len(value) <= limit && !strings.ContainsAny(value, "\r\n\x00")
}
func oneOf(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
func allowedObserverState(value string) bool {
	return oneOf(value, "registered", "connecting", "online", "scanning", "idle", "backoff", "schema_hold", "stopped")
}
func uintString(value uint64) string {
	if value == 0 {
		return "0"
	}
	buffer := [32]byte{}
	position := len(buffer)
	for value > 0 {
		position--
		buffer[position] = byte('0' + value%10)
		value /= 10
	}
	return string(buffer[position:])
}

type orderPageData struct {
	Auction Snapshot
	Orders  orders.DebugSnapshot
}

var orderAuctionTemplateV2 = template.Must(template.New("order-auction-v2").Funcs(template.FuncMap{
	"money": func(value int64) string { return "$" + uintString(uint64(max64Service(0, value))) },
	"clock": func(value time.Time) string {
		if value.IsZero() {
			return "never"
		}
		return value.Local().Format("15:04:05")
	},
}).Parse(orderAuctionHTMLV2))

const orderAuctionHTMLV2 = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><meta http-equiv="refresh" content="2"><title>Order-auction flipper</title>
<style>body{font:14px monospace;max-width:1400px;margin:20px auto;padding:0 14px;color:#ddd;background:#111}h1,h2{color:#fff}table{border-collapse:collapse;width:100%;margin-bottom:22px}th,td{text-align:left;padding:6px;border-bottom:1px solid #333;vertical-align:top}a,code{color:#9cdcfe}.READY{color:#6fda78}.HOLD,.STALE,.REJECTED{color:#ff7777}.RESEARCH,.CAPTURED{color:#f0ca6b}.muted{color:#999}.box{border:1px solid #333;padding:10px;margin:12px 0}</style></head><body>
<nav><a href="/">Auction API</a> · <a href="/order-auction-flipper">Order-auction flipper</a></nav><h1>Order-auction flipper</h1>
<div class="box">Auction API: {{.Auction.Status.State}} · {{.Auction.Status.ValuationCount}} valuations · order observers: {{len .Orders.Observers}} · watches: {{len .Orders.Watches}} · diagnostics (14d): {{.Orders.Diagnostics}}<br><span class="muted">Reference UI only. Each Fabric client keeps its real balance, positions, reserve, and 20/18-slot portfolio locally. No simulated market rows.</span></div>
<div class="box">Order scans: {{.Orders.ScanCoverage.Total}} total · {{.Orders.ScanCoverage.Complete}} complete · {{.Orders.ScanCoverage.Incomplete}} incomplete · {{.Orders.ScanCoverage.UnknownSchema}} unknown schema · {{.Orders.ScanCoverage.DistinctPages}} distinct pages through page {{.Orders.ScanCoverage.HighestPage}} · last {{clock .Orders.ScanCoverage.LastScanAt}}</div>
<h2>Mineflayer observers</h2><table><tr><th>ID</th><th>State</th><th>Parser</th><th>Proxy label</th><th>Task/page</th><th>Latency</th><th>Reconnects</th><th>Last seen</th></tr>{{range .Orders.Observers}}<tr><td>{{.ObserverID}}</td><td>{{.State}}</td><td>{{.ParserVersion}}</td><td>{{.ProxyLabel}}</td><td>{{.CurrentTaskID}} / {{.CurrentPage}}</td><td>{{printf "%.0f" .LatencyMS}}ms</td><td>{{.ReconnectCount}}</td><td>{{clock .LastSeenAt}}</td></tr>{{else}}<tr><td colspan="8" class="muted">No Mineflayer observer has registered yet.</td></tr>{{end}}</table>
<h2>Evidence</h2><table><tr><th>Item</th><th>Tier</th><th>Scans</th><th>Fills/orders</th><th>24h filled / available</th><th>Best reward / queue</th><th>Fresh</th><th>Reason</th></tr>{{range .Orders.Evidence}}<tr><td><code>{{.Signature}}</code></td><td>{{.Tier}}</td><td>{{.CompleteScans}}</td><td>{{.FillEvents}} / {{.DistinctOrders}}</td><td>{{.FilledUnits24h}} / {{.AvailableUnits}}</td><td>{{money .BestUnitReward}} / #{{.BestPricePosition}}</td><td>{{clock .LastSeenAt}}</td><td>{{.Reason}}</td></tr>{{else}}<tr><td colspan="8" class="muted">Waiting for real order snapshots.</td></tr>{{end}}</table>
<h2>Focused watches</h2><table><tr><th>ID</th><th>Signature</th><th>Created</th><th>Expires</th></tr>{{range .Orders.Watches}}<tr><td>{{.ID}}</td><td><code>{{.Signature}}</code></td><td>{{clock .CreatedAt}}</td><td>{{clock .ExpiresAt}}</td></tr>{{else}}<tr><td colspan="4" class="muted">No active focused watches.</td></tr>{{end}}</table>
<h2>Recent confirmed reductions</h2><table><tr><th>Item</th><th>Order</th><th>Observer</th><th>Units</th><th>Unit reward</th><th>Observed</th></tr>{{range .Orders.RecentFills}}<tr><td><code>{{.Signature}}</code></td><td>{{.OrderKey}}</td><td>{{.ObserverID}}</td><td>{{.Units}}</td><td>{{money .UnitReward}}</td><td>{{clock .ObservedAt}}</td></tr>{{else}}<tr><td colspan="6" class="muted">No confirmed quantity reductions yet. Disappearances are not counted.</td></tr>{{end}}</table>
<h2>$10M reference portfolio</h2><table><tr><th>Route</th><th>Item</th><th>Batches</th><th>Capital</th><th>Risk-adjusted/day</th></tr>{{range .Orders.ReferencePortfolio}}<tr><td>{{.Route}}</td><td>{{.ItemName}}</td><td>{{.Batches}}</td><td>{{money .Capital}}</td><td>{{money .RiskAdjustedProfitDay}}</td></tr>{{else}}<tr><td colspan="5" class="muted">No READY candidates fit the reference balance. Real player portfolios remain local to Fabric.</td></tr>{{end}}</table>
<h2>Candidate pool</h2><table><tr><th>State</th><th>Route</th><th>Item/batch</th><th>Capital</th><th>Proceeds</th><th>Conservative profit</th><th>Risk-adjusted/day</th><th>Slots O/A/I</th><th>Volume / queue</th><th>Reason</th></tr>{{range .Orders.Candidates}}<tr><td class="{{.State}}">{{.State}}</td><td>{{.Route}}</td><td>{{.Quantity}}× {{.ItemName}}</td><td>{{money .AcquisitionCost}}</td><td>{{money .ExpectedProceeds}}</td><td>{{money .ConservativeProfit}}</td><td>{{money .RiskAdjustedProfitDay}}</td><td>{{.OrderSlots}} / {{.AuctionSlots}} / {{.InventorySlots}}</td><td>{{.ExecutableBatches}} / #{{.QueuePosition}}</td><td>{{.Reason}}</td></tr>{{else}}<tr><td colspan="10" class="muted">No joined order/API candidates yet.</td></tr>{{end}}</table></body></html>`

func max64Service(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
