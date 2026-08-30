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
	yield, err := s.orders.ShouldYieldDiscovery(r.Context(), value)
	if err != nil {
		s.orderError(w, "check focused watch priority", err)
		return
	}
	if yield {
		w.Header().Set("X-DN-Yield", "focused_watch")
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
		kind := ""
		if value.TaskID != "" {
			var kindErr error
			kind, kindErr = s.orders.AcceptedScanTaskKind(r.Context(), value.ObserverID, value.TaskID)
			if kindErr != nil {
				s.orderError(w, "resolve order scan task", kindErr)
				return
			}
		}
		s.refreshOrderCandidatesIfDue(r.Context(), scanCandidateRefreshInterval(kind, s.cfg.CandidateRefresh))
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "duplicate": !inserted})
}

func scanCandidateRefreshInterval(taskKind string, ordinary time.Duration) time.Duration {
	if taskKind == "focused_watch" && ordinary > 750*time.Millisecond {
		return 750 * time.Millisecond
	}
	return ordinary
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
	lifetime, validLifetime := focusedWatchLifetime(value.DurationSeconds)
	if !safeSignature(value.Signature) || !validLifetime {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid focused watch request"})
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
	watch, err := s.orders.AddWatchFor(r.Context(), value.Signature, lifetime)
	if err != nil {
		s.orderError(w, "add focused watch", err)
		return
	}
	writeJSON(w, http.StatusCreated, watch)
}

func focusedWatchLifetime(seconds int) (time.Duration, bool) {
	if seconds == 0 {
		return time.Minute, true
	}
	if seconds < 10 || seconds > 60 {
		return 0, false
	}
	return time.Duration(seconds) * time.Second, true
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

func (s *Server) dashboardWatch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid form"})
		return
	}
	signature := strings.TrimSpace(r.FormValue("signature"))
	if !safeSignature(signature) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid signature"})
		return
	}
	found := false
	for _, candidate := range s.orders.CandidateFeed().Candidates {
		if candidate.Signature == signature && candidate.Route == "ORDER_TO_AUCTION" && candidate.PriorityRank > 0 {
			found = true
			break
		}
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "ranked order-to-auction candidate is not available"})
		return
	}
	if _, err := s.orders.AddWatch(r.Context(), signature); err != nil {
		s.orderError(w, "add dashboard focused watch", err)
		return
	}
	http.Redirect(w, r, "/order-auction-flipper#good-orders", http.StatusSeeOther)
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

func (s *Server) refreshOrderCandidatesIfDue(ctx context.Context, interval time.Duration) {
	engine := s.engine.Load()
	if engine == nil {
		return
	}
	if _, err := s.orders.RefreshIfDue(ctx, engine, interval); err != nil {
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
		optionalText(value.DisplayName, 128) && value.Quantity >= 1 && value.Quantity <= 64 && value.MaxStackSize >= 1 && value.MaxStackSize <= 64 &&
		value.UnitRewardCents > 0 && value.UnitRewardCents <= 9_000_000_000_000_000_000 && value.RequestedQuantity >= value.RemainingQuantity &&
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
	Auction       Snapshot
	Orders        orders.DebugSnapshot
	Ready         []orders.Candidate
	Research      []orders.Candidate
	Priority      []orders.Candidate
	Immediate     []orders.Candidate
	Blocked       []orders.Candidate
	ReadyCount    int
	ResearchCount int
}

type simpleOrderPageData struct {
	Version       uint64
	GeneratedAt   time.Time
	Ready         []orders.Candidate
	Research      []orders.Candidate
	ReadyCount    int
	CoreCount     int
	FillerCount   int
	ResearchCount int
}

var orderAuctionSimpleTemplate = template.Must(template.New("order-auction-simple").Funcs(template.FuncMap{
	"money":      formatMoney,
	"moneyCents": moneyCents,
	"pct": func(value int) string {
		return strconv.Itoa(value/100) + "." + string([]byte{'0' + byte((value%100)/10), '0' + byte(value%10)}) + "%"
	},
	"clock": func(value time.Time) string {
		if value.IsZero() {
			return "never"
		}
		return value.Local().Format("15:04:05")
	},
	"filler":        func(value orders.Candidate) bool { return value.State == "READY" && value.OrderTier != "actionable" },
	"firstBidCents": firstOrderBidCents,
}).Parse(orderAuctionSimpleHTML))

const orderAuctionSimpleHTML = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Order flips</title>
<style>
*{box-sizing:border-box}body{font:15px system-ui,sans-serif;max-width:1050px;margin:0 auto;padding:24px 18px;color:#e8e8e8;background:#111}header{display:flex;justify-content:space-between;gap:16px;align-items:start;border-bottom:1px solid #333;padding-bottom:14px}h1{font-size:24px;margin:0 0 5px}h2{font-size:17px;margin:28px 0 10px}.muted{color:#999}.status{margin:16px 0;padding:10px 12px;background:#181818;border-left:3px solid #6aa84f}.empty{padding:30px 18px;text-align:center;border:1px solid #333;background:#161616}.flip{border:1px solid #3f5535;background:#151815;margin:10px 0;padding:15px}.top{display:flex;justify-content:space-between;gap:18px;align-items:start}.name{font-size:19px;font-weight:700}.rank{color:#9dce7b}.numbers{display:grid;grid-template-columns:repeat(4,minmax(140px,1fr));gap:8px;margin:14px 0}.number{padding:9px;background:#1d1d1d}.number strong{display:block;font-size:17px;color:#fff}.profit strong{color:#86d66b}.actions{display:flex;gap:10px;align-items:center;flex-wrap:wrap}button{font:inherit;color:#fff;background:#2d5121;border:1px solid #6a9558;padding:7px 11px;cursor:pointer}a,code{color:#9dce7b}code{background:#1d1d1d;padding:3px 5px}details{margin-top:28px;border-top:1px solid #333;padding-top:13px}summary{cursor:pointer;color:#bbb}.research{display:grid;grid-template-columns:1fr auto;gap:8px;padding:9px 0;border-bottom:1px solid #292929}.warn{color:#e0bd62}@media(max-width:760px){.numbers{grid-template-columns:1fr 1fr}.top,header{display:block}header a{display:inline-block;margin-top:8px}}@media(max-width:430px){.numbers{grid-template-columns:1fr}}
</style></head><body>
<header><div><h1>Order → Auction Flips</h1><div class="muted">Goal: keep 20 distinct profitable offers active. Fabric performs a focused recheck before every creation.</div></div><a href="/order-auction-flipper/debug">Debug</a></header>
<div id="live" data-version="{{.Version}}"><div class="status"><strong>{{.ReadyCount}} ready</strong> · {{.CoreCount}} core · {{.FillerCount}} filler · full market frontier; Fabric applies your live balance and slots · updated {{clock .GeneratedAt}} · live</div>
<main id="good-orders">
{{range .Ready}}<article class="flip"><div class="top"><div><span class="rank">#{{.PriorityRank}}</span> <span class="name">{{.Quantity}}× {{.ItemName}}</span><div class="muted"><code>{{.ItemID}}</code></div></div><div><strong>{{if filler .}}FILLER READY{{else}}CORE READY{{end}}</strong><div class="muted">{{if filler .}}one-stack starter; cancel when displaced{{else}}measured fills; scalable{{end}}</div></div></div>
<div class="numbers"><div class="number"><span class="muted">Latest /orders display</span><strong>{{moneyCents .ObservedOrderUnitRewardCents}} each</strong><span class="muted">sampled {{clock .ResearchFreshAt}} · Fabric starts at {{moneyCents (firstBidCents .)}} · reserved cap {{moneyCents .OrderUnitRewardCents}}; rank is verified after creation</span></div><div class="number"><span class="muted">Each auction exit</span><strong>{{.Quantity}}× list for {{money .TargetListPrice}}</strong><span class="muted">{{money .ExpectedProceeds}} conservative proceeds after fee · reuse the 18 slots</span></div><div class="number profit"><span class="muted">Profit per exit listing</span><strong>+{{money .ConservativeProfit}}</strong><span class="muted">{{pct .MarginBPS}} ROI at the reserved cap; the first bid is cheaper</span></div><div class="number"><span class="muted">Total market opportunity</span><strong>{{money .PriorityScore}} / day</strong><span class="muted">one order per item · {{.AuctionVolume24h}} sales{{if .Profiled}} · proven profile{{end}}</span></div></div>
<div class="actions"><form method="post" action="/order-auction-flipper/watch"><input type="hidden" name="signature" value="{{.Signature}}"><button type="submit">Recheck now</button></form><code>{{.OrderCommand}}</code><span>then</span><code>{{.AuctionCommand}}</code><span class="muted">research {{clock .ResearchFreshAt}} · focused {{clock .FocusedFreshAt}}</span></div></article>
{{else}}<div class="empty"><strong>No current profitable offers.</strong><div class="muted">The collector must be online and rebuild current order evidence. Retained profiles stay in the fast recheck rotation.</div></div>{{end}}
</main>
<details><summary>Research queue ({{.ResearchCount}}) — not ready to buy</summary>{{range .Research}}<div class="research"><div><strong>{{.Quantity}}× {{.ItemName}}</strong> · modeled +{{money .ConservativeProfit}}{{if .Profiled}} · proven profile, fast recheck eligible{{end}}<div class="warn">{{.Reason}}</div></div><form method="post" action="/order-auction-flipper/watch"><input type="hidden" name="signature" value="{{.Signature}}"><button type="submit">Research this</button></form></div>{{else}}<p class="muted">No current research candidates.</p>{{end}}</details></div>
<script>let updating=false;async function update(){if(updating||document.hidden)return;updating=true;try{const current=document.getElementById('live');const response=await fetch(location.pathname,{cache:'no-cache',headers:{Accept:'text/html','If-None-Match':'"order-page-'+current.dataset.version+'"'}});if(response.status===304||!response.ok)return;const documentCopy=new DOMParser().parseFromString(await response.text(),'text/html');const next=documentCopy.getElementById('live');if(next&&next.dataset.version!==current.dataset.version){const open=!!current.querySelector('details[open]');current.replaceWith(next);if(open)next.querySelector('details')?.setAttribute('open','')}}catch(error){}finally{updating=false}}setInterval(update,1000);document.addEventListener('visibilitychange',()=>{if(!document.hidden)update()});</script>
</body></html>`

var orderAuctionTemplateV2 = template.Must(template.New("order-auction-v2").Funcs(template.FuncMap{
	"money":      formatMoney,
	"moneyCents": moneyCents,
	"pct": func(value int) string {
		return strconv.Itoa(value/100) + "." + string([]byte{'0' + byte((value%100)/10), '0' + byte(value%10)}) + "%"
	},
	"age": func(seconds int64) string {
		if seconds < 60 {
			return strconv.FormatInt(seconds, 10) + "s"
		}
		if seconds < 3600 {
			return strconv.FormatInt(seconds/60, 10) + "m"
		}
		return strconv.FormatInt(seconds/3600, 10) + "h"
	},
	"clock": func(value time.Time) string {
		if value.IsZero() {
			return "never"
		}
		return value.Local().Format("15:04:05")
	},
	"readyKind": func(value orders.Candidate) string {
		if value.State == "READY" && value.OrderTier != "actionable" {
			return "FILLER READY"
		}
		return value.State
	},
}).Parse(orderAuctionHTMLV2))

const orderAuctionHTMLV2 = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Order → auction priorities</title>
<style>
body{font:14px ui-monospace,SFMono-Regular,Consolas,monospace;max-width:1550px;margin:16px auto;padding:0 14px;color:#ddd;background:#101010}h1,h2{color:#fff;margin:18px 0 8px}h1{font-size:22px}h2{font-size:17px}a,code{color:#9cdcfe}nav{position:sticky;top:0;background:#101010;padding:9px 0;border-bottom:1px solid #444;z-index:2}nav a{margin-right:14px}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:8px;margin:10px 0}.card,.box{border:1px solid #3a3a3a;padding:9px;background:#151515}.metric{font-size:18px;color:#fff}.muted{color:#999}.good,.READY{color:#72d47d}.warn,.RESEARCH,.CAPTURED{color:#e5c45e}.bad,.HOLD,.STALE,.REJECTED{color:#ff7a7a}table{border-collapse:collapse;width:100%;margin:8px 0 18px}th,td{text-align:left;padding:6px;border-bottom:1px solid #333;vertical-align:top;white-space:nowrap}th{position:sticky;top:38px;background:#171717}td.wrap{white-space:normal;min-width:220px}.rank{font-size:16px;color:#fff}button,input{font:inherit;color:#ddd;background:#222;border:1px solid #555;padding:4px 7px}button{cursor:pointer}button:hover{border-color:#aaa}.controls{display:flex;gap:10px;align-items:center;flex-wrap:wrap;margin:8px 0}.scroll{overflow:auto;max-height:720px;border:1px solid #2d2d2d}details{border-top:1px solid #333;padding-top:8px;margin-top:16px}summary{cursor:pointer;color:#fff;font-size:16px}.route{color:#b8a1ff}.flags{color:#ffb2b2;font-size:12px}
</style></head><body>
<nav><a href="#priority">Priority</a><a href="#immediate">Auction → order</a><a href="#operations">Operations</a><a href="#evidence">Evidence</a><a href="/">Auction API debug</a></nav>
<h1>Order → auction priority queue</h1>
<p class="muted">Create the order at the shown competitive unit reward, then relist the exact shown batch quantity. CORE READY has measured fills and scales; FILLER READY is a stable, exactly priced one-stack offer used while a stronger market is unavailable. Both receive a final focused recheck. RESEARCH is not actionable.</p>
<div class="grid">
<div class="card"><div class="metric good">{{.ReadyCount}} READY</div><div class="muted">eligible now</div></div>
<div class="card"><div class="metric warn">{{.ResearchCount}} RESEARCH</div><div class="muted">ranked, needs evidence</div></div>
<div class="card"><div class="metric">{{.Auction.Status.ValuationCount}}</div><div class="muted">auction valuations</div></div>
<div class="card"><div class="metric">{{.Orders.ScanCoverage.CompleteSignatures}}</div><div class="muted">current base-safe order items</div></div>
<div class="card"><div class="metric">{{.Orders.ScanCoverage.ConfirmedFills}}</div><div class="muted">confirmed reductions</div></div>
<div class="card"><div class="metric">{{.Orders.ScanCoverage.QuarantinedFills}}</div><div class="muted">legacy reductions quarantined</div></div>
</div>
<div class="controls"><label>Filter <input id="filter" type="search" placeholder="item or state"></label><button type="button" onclick="location.reload()">Refresh now</button><label><input id="auto" type="checkbox"> auto-refresh 5s</label><span class="muted">generated {{clock .Orders.GeneratedAt}}</span></div>
<section id="priority"><h2>Highest-priority order → auction candidates</h2>
<div class="scroll"><table id="priority-table"><thead><tr><th>#</th><th>State</th><th>Item / exact batch</th><th>Create order</th><th>Relist auction</th><th>Conservative profit</th><th>ROI</th><th>Priority/day</th><th>Capacity</th><th>Evidence</th><th>Action</th></tr></thead><tbody>
{{range .Priority}}<tr data-row="{{.ItemName}} {{.Signature}} {{.State}}"><td class="rank">{{.PriorityRank}}</td><td class="{{.State}}">{{readyKind .}}</td><td><strong>{{.Quantity}}× {{.ItemName}}</strong><br><code>{{.ItemID}}</code></td><td><strong>{{moneyCents .OrderUnitRewardCents}} each</strong><br><span class="muted">visible {{moneyCents .ObservedOrderUnitRewardCents}} · {{money .AcquisitionCost}} per exit batch · targets queue #{{.QueuePosition}}</span></td><td><strong>{{money .TargetListPrice}} list</strong><br><span class="muted">{{money .ExpectedProceeds}} conservative after-fee proceeds · {{.AuctionVolume24h}} sales / {{.AuctionSellerCount}} sellers</span></td><td>{{money .ConservativeProfit}}<br><span class="muted">modeled net {{money .GrossProfit}}</span></td><td>{{pct .MarginBPS}}</td><td><strong>{{money .PriorityScore}}</strong><br><span class="muted">{{money .RiskAdjustedProfitDay}} / exit batch</span></td><td>{{.MaxOrderQuantity}} units max<br><span class="muted">{{.ExecutableBatches}} sequential exits{{if .Profiled}} · profiled{{end}}</span></td><td class="wrap">{{.OrderFilledUnits24h}} confirmed units · confidence {{pct .ConfidenceBPS}} · completion {{pct .CompletionBPS}} · volatility {{pct .VolatilityBPS}} · refs {{age .ReferenceAgeSeconds}}{{range .RiskFlags}}<br><span class="flags">{{.}}</span>{{end}}{{if .Reason}}<br><span class="warn">{{.Reason}}</span>{{end}}</td><td><form method="post" action="/order-auction-flipper/watch"><input type="hidden" name="signature" value="{{.Signature}}"><button type="submit">Focused watch</button></form><a href="/api/v1/debug/valuation?signature={{urlquery .Signature}}">valuation JSON</a><br><code>{{.OrderCommand}}</code><br><code>{{.AuctionCommand}}</code></td></tr>{{else}}<tr><td colspan="11" class="muted">No profitable, base-safe order → auction candidates yet. The collector is rebuilding trusted evidence.</td></tr>{{end}}
</tbody></table></div></section>
<section id="immediate"><h2>Auction → existing order</h2><p class="muted">Secondary route. These buy an auction and manually fulfill an observed order; they do not consume a new order or auction listing slot.</p><table><tr><th>#</th><th>State</th><th>Item</th><th>Cost</th><th>Order proceeds</th><th>Profit</th><th>Priority/day</th><th>Capacity</th><th>Reason</th></tr>{{range .Immediate}}<tr><td>{{.PriorityRank}}</td><td class="{{.State}}">{{.State}}</td><td>{{.Quantity}}× {{.ItemName}}</td><td>{{money .AcquisitionCost}}</td><td>{{money .ExpectedProceeds}}</td><td>{{money .ConservativeProfit}}</td><td>{{money .PriorityScore}}</td><td>{{.ExecutableBatches}}</td><td class="wrap">{{.Reason}}</td></tr>{{else}}<tr><td colspan="9" class="muted">No ranked immediate route.</td></tr>{{end}}</table></section>
<section id="operations"><h2>Operations</h2><div class="box">Auction API: <span class="{{.Auction.Status.State}}">{{.Auction.Status.State}}</span> · observer count {{len .Orders.Observers}} · active watches {{len .Orders.Watches}} · last order scan {{clock .Orders.ScanCoverage.LastScanAt}} · recent pages 1–{{.Orders.ScanCoverage.HighestPage}} · {{.Orders.ScanCoverage.Incomplete}} incomplete · {{.Orders.ScanCoverage.UnknownSchema}} unknown schema</div>
<table><tr><th>Observer</th><th>State</th><th>Parser</th><th>Proxy</th><th>Task / page</th><th>Latency</th><th>Reconnects</th><th>Last seen</th></tr>{{range .Orders.Observers}}<tr><td>{{.ObserverID}}</td><td>{{.State}}</td><td>{{.ParserVersion}}</td><td>{{.ProxyLabel}}</td><td>{{.CurrentTaskID}} / {{.CurrentPage}}</td><td>{{printf "%.0f" .LatencyMS}}ms</td><td>{{.ReconnectCount}}</td><td>{{clock .LastSeenAt}}</td></tr>{{else}}<tr><td colspan="8" class="bad">No observer registered.</td></tr>{{end}}</table>
<h2>Focused watches</h2><table><tr><th>Signature</th><th>Created</th><th>Expires</th></tr>{{range .Orders.Watches}}<tr><td><code>{{.Signature}}</code></td><td>{{clock .CreatedAt}}</td><td>{{clock .ExpiresAt}}</td></tr>{{else}}<tr><td colspan="3" class="muted">No active watch. Start one from the priority table to validate fill velocity.</td></tr>{{end}}</table></section>
<details id="evidence"><summary>Order evidence and rejection details</summary><div class="scroll"><table><tr><th>Item</th><th>Tier</th><th>Scans</th><th>Confirmed fills / orders</th><th>24h filled / available</th><th>Visible / first-place bid</th><th>Research seen</th><th>Focused seen</th><th>Reason</th></tr>{{range .Orders.Evidence}}<tr data-row="{{.DisplayName}} {{.Signature}} {{.Tier}}"><td>{{.DisplayName}}<br><code>{{.Signature}}</code></td><td class="{{.Tier}}">{{.Tier}}</td><td>{{.CompleteScans}}</td><td>{{.FillEvents}} / {{.DistinctOrders}}</td><td>{{.FilledUnits24h}} / {{.AvailableUnits}}</td><td>{{moneyCents .BestUnitRewardCents}} → {{moneyCents .BestCompetitiveUnitRewardCents}}</td><td>{{clock .LastSeenAt}}</td><td>{{clock .FocusedSeenAt}}</td><td class="wrap">{{.Reason}}</td></tr>{{else}}<tr><td colspan="9" class="muted">Waiting for trusted order snapshots.</td></tr>{{end}}</table></div></details>
<details><summary>Blocked and stale candidate diagnostics</summary><div class="scroll"><table><tr><th>State</th><th>Route</th><th>Item</th><th>Profit</th><th>Priority</th><th>Reason</th><th>Risks</th></tr>{{range .Blocked}}<tr><td class="{{.State}}">{{.State}}</td><td>{{.Route}}</td><td>{{.Quantity}}× {{.ItemName}}</td><td>{{money .ConservativeProfit}}</td><td>{{money .PriorityScore}}</td><td class="wrap">{{.Reason}}</td><td class="wrap">{{range .RiskFlags}}{{.}} {{end}}</td></tr>{{else}}<tr><td colspan="7" class="muted">None.</td></tr>{{end}}</table></div></details>
<details><summary>Recent confirmed short-gap reductions</summary><table><tr><th>Item</th><th>Order</th><th>Units</th><th>Unit reward</th><th>Interval</th><th>Observed</th></tr>{{range .Orders.RecentFills}}<tr><td><code>{{.Signature}}</code></td><td>{{.OrderKey}}</td><td>{{.Units}}</td><td>{{moneyCents .UnitRewardCents}}</td><td>{{clock .PreviousObservedAt}} → {{clock .ObservedAt}}</td><td>{{clock .ObservedAt}}</td></tr>{{else}}<tr><td colspan="6" class="muted">No confirmed reductions yet. Historical long-gap reductions are quarantined and disappearances never count.</td></tr>{{end}}</table></details>
<script>
const filter=document.getElementById('filter'),auto=document.getElementById('auto');
filter.value=sessionStorage.getItem('orderFilter')||'';
function applyFilter(){const q=filter.value.toLowerCase();document.querySelectorAll('[data-row]').forEach(row=>row.hidden=q&&!row.dataset.row.toLowerCase().includes(q));sessionStorage.setItem('orderFilter',filter.value)}
filter.addEventListener('input',applyFilter);applyFilter();
const autoSaved=localStorage.getItem('orderAutoRefresh');auto.checked=autoSaved===null||autoSaved==='1';auto.addEventListener('change',()=>localStorage.setItem('orderAutoRefresh',auto.checked?'1':'0'));
const saved=Number(sessionStorage.getItem('orderScroll')||0);if(saved)scrollTo(0,saved);setInterval(()=>{if(auto.checked){sessionStorage.setItem('orderScroll',String(scrollY));location.reload()}},5000);
</script></body></html>`

func max64Service(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func firstOrderBidCents(value orders.Candidate) int64 {
	if value.ObservedOrderUnitRewardCents < value.OrderUnitRewardCents {
		return value.ObservedOrderUnitRewardCents + 1
	}
	return value.OrderUnitRewardCents
}

func formatMoney(value int64) string {
	return "$" + groupedUint(uint64(max64Service(0, value)))
}

func groupedUint(value uint64) string {
	raw := uintString(value)
	if len(raw) <= 3 {
		return raw
	}
	first := len(raw) % 3
	if first == 0 {
		first = 3
	}
	var out strings.Builder
	out.Grow(len(raw) + len(raw)/3)
	out.WriteString(raw[:first])
	for index := first; index < len(raw); index += 3 {
		out.WriteByte(',')
		out.WriteString(raw[index : index+3])
	}
	return out.String()
}

func moneyCents(value int64) string {
	value = max64Service(0, value)
	dollars, cents := value/100, value%100
	return "$" + groupedUint(uint64(dollars)) + "." + string([]byte{'0' + byte(cents/10), '0' + byte(cents%10)})
}
