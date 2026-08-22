package platform

import (
	"bytes"
	"donut-network/internal/market"
	"donut-network/internal/network"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestVerticalSlice(t *testing.T) {
	s := NewServer(Config{WorkerToken: "test-token", CollectorToken: "test-token", DataMode: "simulation"})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	now := time.Now().UTC()
	ts := []market.Transaction{}
	for i := 0; i < 20; i++ {
		ts = append(ts, market.Transaction{SellerName: "history", Item: market.Item{ID: "minecraft:elytra", Quantity: 1}, TotalPrice: 300_000_000 + int64(i)*10_000, SoldAt: now.Add(-time.Duration(i) * time.Minute), Source: market.SourceSimulator})
	}
	post(t, srv.URL+"/api/v1/ingest/transactions", "test-token", ts, http.StatusAccepted)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?token=test-token"
	header := http.Header{"X-Data-Mode": []string{"simulation"}}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	kind, data, err := conn.ReadMessage()
	if err != nil || kind != websocket.BinaryMessage {
		t.Fatalf("snapshot read: %v kind=%d", err, kind)
	}
	frame, err := network.Decode(data)
	if err != nil || frame.Type != network.MsgSnapshot {
		t.Fatalf("frame=%+v err=%v", frame, err)
	}
	var snap market.Snapshot
	if err := json.Unmarshal(frame.Payload, &snap); err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.Valuations["minecraft:elytra"]; !ok {
		t.Fatalf("missing valuation: %+v", snap)
	}
	listing := market.NormalizeListing(market.Listing{SellerName: "flip", Item: market.Item{ID: "elytra", Quantity: 1}, TotalPrice: 200_000_000, Source: market.SourceSimulator})
	post(t, srv.URL+"/api/v1/observations", "test-token", listing, http.StatusAccepted)
	event := network.TelemetryEvent{ClientID: "worker-1", Kind: "PURCHASE_SUCCESS", Fingerprint: listing.Fingerprint, Success: true, ObservedAt: now.UnixMilli(), Metadata: map[string]string{"reason": "simulated"}}
	post(t, srv.URL+"/api/v1/telemetry", "test-token", event, http.StatusAccepted)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/dashboard", nil)
	req.Header.Set("Authorization", "Bearer local-admin-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var d Dashboard
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		t.Fatal(err)
	}
	if len(d.Listings) != 1 || len(d.Purchases) != 1 || !d.Purchases[0].Success {
		t.Fatalf("pipeline incomplete: %+v", d)
	}
}

func TestLiveModeRejectsSimulatedDataAndWorkers(t *testing.T) {
	s := NewServer(Config{WorkerToken: "test-token", CollectorToken: "test-token", DataMode: "live"})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	listing := market.Listing{SellerName: "synthetic", Item: market.Item{ID: "minecraft:elytra", Quantity: 1}, TotalPrice: 1, Source: market.SourceSimulator}
	post(t, srv.URL+"/api/v1/ingest/listings", "test-token", []market.Listing{listing}, http.StatusConflict)
	post(t, srv.URL+"/api/v1/observations", "test-token", listing, http.StatusConflict)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	header := http.Header{"Authorization": []string{"Bearer test-token"}, "X-Data-Mode": []string{"simulation"}}
	conn, response, err := websocket.DefaultDialer.Dial(wsURL, header)
	if conn != nil {
		conn.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusConflict {
		t.Fatalf("simulated worker accepted by live backend: err=%v response=%v", err, response)
	}
	if got := len(s.engine.Listings(10)); got != 0 {
		t.Fatalf("live engine contains %d simulated listings", got)
	}
}
func TestAuthenticationAndBodyLimit(t *testing.T) {
	s := NewServer(Config{WorkerToken: "secret"})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/observations", bytes.NewReader([]byte(`{}`)))
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401 got %d", resp.StatusCode)
	}
	resp.Body.Close()
	big := strings.Repeat("x", network.MaxFrameSize+1)
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/observations", strings.NewReader(big))
	req.Header.Set("Authorization", "Bearer secret")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 400 && resp.StatusCode != 413 {
		t.Fatalf("expected rejected oversize body, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestOperatorEndpointsRequireAdminToken(t *testing.T) {
	s := NewServer(Config{WorkerToken: "worker", AdminToken: "admin"})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	for _, path := range []string{"/api/v1/dashboard", "/metrics"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s status=%d want=%d", path, resp.StatusCode, http.StatusUnauthorized)
		}
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer admin")
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s admin status=%d want=%d", path, resp.StatusCode, http.StatusOK)
		}
	}
}

func TestWebSocketChatRateLimit(t *testing.T) {
	c := &wsClient{}
	now := time.Now()
	for i := 0; i < 20; i++ {
		if !c.allow(network.MsgChat, now) {
			t.Fatalf("message %d rejected early", i)
		}
	}
	if c.allow(network.MsgChat, now) {
		t.Fatal("chat flood was accepted")
	}
	if !c.allow(network.MsgListingObserved, now) {
		t.Fatal("chat flood blocked market-critical traffic")
	}
}
func TestWebSocketRejectsUntrustedOrigin(t *testing.T) {
	s := NewServer(Config{WorkerToken: "test-token"})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	header := http.Header{"Authorization": []string{"Bearer test-token"}, "Origin": []string{"https://evil.invalid"}}
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if conn != nil {
		conn.Close()
	}
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("untrusted origin accepted: err=%v response=%v", err, resp)
	}
}

func TestRateLimitIdentityMapIsBounded(t *testing.T) {
	s := NewServer(Config{WorkerToken: "test-token"})
	now := time.Now()
	for i := 0; i < 10_000; i++ {
		s.limits[fmt.Sprintf("client-%d", i)] = &rateWindow{start: now, count: 1}
	}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Client-ID", "new-client")
	if s.allow(r, 240, time.Minute) {
		t.Fatal("rate-limit identity map grew beyond cap")
	}
}

func TestLargeSnapshotsAreChunkedWithinProtocolLimit(t *testing.T) {
	valuations := make(map[string]market.Valuation, 500)
	for i := 0; i < 500; i++ {
		signature := fmt.Sprintf("minecraft:test_item_%04d|name=a_long_but_realistic_modifier_name", i)
		valuations[signature] = market.Valuation{Signature: signature, FairValue: int64(i + 1), RiskFlags: []string{"low_liquidity", "api_modifier_blindspot"}}
	}
	frames, err := encodeSnapshotFrames(market.Snapshot{Version: 10, GeneratedAt: time.Now(), Valuations: valuations})
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) < 2 {
		t.Fatalf("expected chunking, got %d frame", len(frames))
	}
	for _, encoded := range frames {
		frame, err := network.Decode(encoded)
		if err != nil || frame.Type != network.MsgSnapshotChunk {
			t.Fatalf("invalid chunk frame: type=%d err=%v bytes=%d", frame.Type, err, len(encoded))
		}
	}
}

func TestCollectorStatusDebugAndCompactSnapshot(t *testing.T) {
	s := NewServer(Config{WorkerToken: "worker", CollectorToken: "collector", AdminToken: "admin", DataMode: "simulation"})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	now := time.Now().UTC()
	status := market.CollectorStatus{State: "ready", CycleStartedAt: now.Add(-time.Second), CycleCompletedAt: now, LastSuccessAt: now, ListingsFetched: 44, TransactionsFetched: 3, APIRequests: 5}
	post(t, srv.URL+"/api/v1/ingest/status", "collector", status, http.StatusAccepted)
	transactions := []market.Transaction{
		{SellerName: "a", Item: market.Item{ID: "minecraft:diamond", Quantity: 1}, TotalPrice: 100, SoldAt: now, Source: market.SourceSimulator},
		{SellerName: "b", Item: market.Item{ID: "minecraft:diamond", Quantity: 1}, TotalPrice: 101, SoldAt: now.Add(-time.Minute), Source: market.SourceSimulator},
		{SellerName: "c", Item: market.Item{ID: "minecraft:diamond", Quantity: 1}, TotalPrice: 102, SoldAt: now.Add(-2 * time.Minute), Source: market.SourceSimulator},
	}
	post(t, srv.URL+"/api/v1/ingest/transactions", "collector", transactions, http.StatusAccepted)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/debug/valuation?signature=minecraft:diamond", nil)
	req.Header.Set("Authorization", "Bearer admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var debug market.ValuationDebug
	if err := json.NewDecoder(resp.Body).Decode(&debug); err != nil {
		resp.Body.Close()
		t.Fatal(err)
	}
	resp.Body.Close()
	if debug.Status != "ready" || debug.Valuation == nil || debug.RecentRawCount != 3 {
		t.Fatalf("debug=%+v", debug)
	}

	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/client-snapshot", nil)
	req.Header.Set("Authorization", "Bearer worker")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	etag := resp.Header.Get("ETag")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || etag == "" {
		t.Fatalf("snapshot status=%d etag=%q", resp.StatusCode, etag)
	}
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/client-snapshot", nil)
	req.Header.Set("Authorization", "Bearer worker")
	req.Header.Set("If-None-Match", etag)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional snapshot status=%d", resp.StatusCode)
	}
}

func TestClientOpportunitiesEndpointReturnsBackendRankedFlip(t *testing.T) {
	s := NewServer(Config{WorkerToken: "worker", CollectorToken: "collector", DataMode: "simulation"})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	now := time.Now().UTC()
	transactions := make([]market.Transaction, 20)
	for i := range transactions {
		transactions[i] = market.Transaction{
			SellerName: fmt.Sprintf("history-%d", i), Item: market.Item{ID: "minecraft:diamond", Quantity: 1},
			TotalPrice: 10_000_000 + int64(i%3)*10_000, SoldAt: now.Add(-time.Duration(i) * time.Minute), Source: market.SourceSimulator,
		}
	}
	post(t, srv.URL+"/api/v1/ingest/transactions", "collector", transactions, http.StatusAccepted)
	post(t, srv.URL+"/api/v1/ingest/listings", "collector", []market.Listing{{
		AuthoritativeID: "auction-123", SellerName: "flip-seller", Item: market.Item{ID: "minecraft:diamond", Quantity: 1},
		TotalPrice: 2_000_000, ExpiresAt: now.Add(time.Hour), Source: market.SourceSimulator,
	}}, http.StatusAccepted)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/opportunities", nil)
	req.Header.Set("Authorization", "Bearer worker")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var feed ClientOpportunityFeed
	if err := json.NewDecoder(resp.Body).Decode(&feed); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || len(feed.Opportunities) != 1 {
		t.Fatalf("status=%d feed=%+v", resp.StatusCode, feed)
	}
	got := feed.Opportunities[0]
	if got.AuthoritativeID != "auction-123" || got.Profit < 1_000_000 || got.MarginBPS < 1_000 {
		t.Fatalf("unexpected opportunity: %+v", got)
	}
}

func TestLiveOpportunityFeedFailsClosedWhenCollectorIsStale(t *testing.T) {
	s := NewServer(Config{WorkerToken: "worker", DataMode: "live"})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/opportunities", nil)
	req.Header.Set("Authorization", "Bearer worker")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var feed ClientOpportunityFeed
	if err := json.NewDecoder(resp.Body).Decode(&feed); err != nil {
		t.Fatal(err)
	}
	if feed.State != "stale" || len(feed.Opportunities) != 0 {
		t.Fatalf("stale live feed did not fail closed: %+v", feed)
	}
}

func TestIngestRejectsSemanticallyInvalidMarketData(t *testing.T) {
	s := NewServer(Config{CollectorToken: "collector", DataMode: "live"})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	invalid := []market.Transaction{{SellerName: strings.Repeat("x", 65), Item: market.Item{ID: "minecraft:diamond", Quantity: 1}, TotalPrice: -1, SoldAt: time.Now(), Source: market.SourceDonutAPI}}
	post(t, srv.URL+"/api/v1/ingest/transactions", "collector", invalid, http.StatusBadRequest)
	if len(s.engine.Transactions(10)) != 0 {
		t.Fatal("invalid transaction reached the engine")
	}
}
func post(t *testing.T, url, token string, v any, want int) {
	t.Helper()
	body, _ := json.Marshal(v)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("%s status=%d want=%d", url, resp.StatusCode, want)
	}
}
