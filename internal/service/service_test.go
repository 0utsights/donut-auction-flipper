package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"donut-network/internal/donutapi"
	"donut-network/internal/market"
	"donut-network/internal/orders"
)

type fakeUpstream struct {
	transactions []market.Transaction
	listings     []market.Listing
	err          error
	pages        int
	pageStarted  chan<- struct{}
	pageRelease  <-chan struct{}
}

func (f *fakeUpstream) AllTransactionPages(context.Context) ([]market.Transaction, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.transactions, nil
}
func (f *fakeUpstream) AuctionPage(context.Context, int, string, string) ([]market.Listing, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.pageStarted != nil {
		f.pageStarted <- struct{}{}
	}
	if f.pageRelease != nil {
		<-f.pageRelease
	}
	f.pages++
	return f.listings, nil
}
func (*fakeUpstream) Stats() donutapi.Stats { return donutapi.Stats{Requests: 2} }

type memoryHistory struct{ values []market.Transaction }

func (m *memoryHistory) Load() ([]market.Transaction, error) { return m.values, nil }
func (m *memoryHistory) Save(values []market.Transaction) error {
	m.values = append([]market.Transaction(nil), values...)
	return nil
}

func TestCollectBuildsAuthenticatedFlipFeed(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	item := market.Item{ID: "minecraft:diamond", Quantity: 1, DisplayName: "Diamond"}
	transactions := make([]market.Transaction, 0, 12)
	for index := 0; index < 12; index++ {
		transactions = append(transactions, market.Transaction{SellerName: fmt.Sprintf("seller-%d", index), Item: item, TotalPrice: 1_000_000 + int64(index*1_000), SoldAt: now.Add(-time.Duration(index+1) * time.Hour), Source: market.SourceDonutAPI})
	}
	listing := market.NormalizeListing(market.Listing{AuthoritativeID: "auction-1", SellerName: "cheap", Item: item, TotalPrice: 500_000, LastSeen: now, ExpiresAt: now.Add(time.Hour), Source: market.SourceDonutAPI})
	upstream := &fakeUpstream{transactions: transactions, listings: []market.Listing{listing}}
	server, err := New(Config{Address: "127.0.0.1:8080", ClientToken: "client-secret", ListingPages: 2, Thresholds: market.Thresholds{MinProfit: 1, MinMarginBPS: 1, MinConfidenceBPS: 1, MinVolume24h: 1}}, upstream, &memoryHistory{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	server.now = func() time.Time { return now }
	if err := server.CollectOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if upstream.pages != 1 {
		t.Fatalf("short page should end scan, pages=%d", upstream.pages)
	}
	snapshot := server.Snapshot()
	if snapshot.Status.State != "ready" || snapshot.Version != 1 || len(snapshot.Flips) != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if snapshot.Analysis.Qualified != 1 || len(snapshot.Valuations) == 0 {
		t.Fatalf("missing debug analysis: %+v", snapshot)
	}
	if snapshot.Flips[0].SearchCommand != "/ah cheap" || snapshot.Flips[0].SellerCommand != "/ah cheap" || snapshot.Flips[0].ItemCommand != "/ah diamond" {
		t.Fatalf("unsafe/wrong search commands: %+v", snapshot.Flips[0])
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/flips", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated code=%d", response.Code)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/flips", nil)
	request.Header.Set("Authorization", "Bearer client-secret")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("ETag") != "\"1-ready\"" {
		t.Fatalf("feed code=%d etag=%q", response.Code, response.Header().Get("ETag"))
	}
	var payload struct {
		Flips []Flip `json:"flips"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || len(payload.Flips) != 1 {
		t.Fatalf("bad payload: %s error=%v", response.Body.String(), err)
	}
	if payload.Flips[0].UnitReference <= 0 || payload.Flips[0].SingularUnitRef != payload.Flips[0].UnitReference || payload.Flips[0].QuantityUnitRef != payload.Flips[0].UnitReference || payload.Flips[0].PricingBasis != "exact-quantity" || payload.Flips[0].ModelVersion != market.QuantityValuationModelVersion {
		t.Fatalf("quantity pricing audit fields missing: %+v", payload.Flips[0])
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/flips", nil)
	request.Header.Set("Authorization", "Bearer client-secret")
	request.Header.Set("If-None-Match", "\"1-ready\"")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotModified {
		t.Fatalf("conditional response=%d", response.Code)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/debug/valuation?signature=minecraft:diamond", nil)
	request.Header.Set("Authorization", "Bearer client-secret")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "quick_sell_value") {
		t.Fatalf("valuation debug code=%d body=%s", response.Code, response.Body.String())
	}
}

func TestFailurePreservesPreviousFeed(t *testing.T) {
	upstream := &fakeUpstream{err: errors.New("upstream unavailable")}
	server, err := New(Config{Address: "127.0.0.1:8080"}, upstream, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.CollectOnce(context.Background()); err == nil {
		t.Fatal("expected collection error")
	}
	if server.Snapshot().Status.State != "error" {
		t.Fatalf("status=%+v", server.Snapshot().Status)
	}
}

func TestFastCollectionPublishesNewestPageFromStoredHistory(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	item := market.Item{ID: "minecraft:diamond", Quantity: 1, DisplayName: "Diamond"}
	transactions := make([]market.Transaction, 0, 12)
	for index := 0; index < 12; index++ {
		transactions = append(transactions, market.Transaction{
			SellerName: fmt.Sprintf("seller-%d", index), Item: item, TotalPrice: 1_000_000,
			SoldAt: now.Add(-time.Duration(index) * time.Minute), Source: market.SourceDonutAPI,
		})
	}
	listing := market.Listing{AuthoritativeID: "fast-auction", SellerName: "cheap", Item: item,
		TotalPrice: 500_000, ExpiresAt: now.Add(time.Hour), Source: market.SourceDonutAPI}
	upstream := &fakeUpstream{listings: []market.Listing{listing}}
	server, err := New(Config{FastInterval: 250 * time.Millisecond, Thresholds: market.Thresholds{
		MinProfit: 1, MinMarginBPS: 1, MinConfidenceBPS: 1, MinVolume24h: 1,
	}}, upstream, &memoryHistory{values: transactions}, nil)
	if err != nil {
		t.Fatal(err)
	}
	server.now = func() time.Time { return now }
	if err := server.CollectFastOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot := server.Snapshot()
	if snapshot.Status.State != "ready" || snapshot.Version != 1 || len(snapshot.Flips) != 1 {
		t.Fatalf("fast snapshot was not published: %+v", snapshot)
	}
	if snapshot.Status.FastListingsFetched != 1 || !snapshot.Status.FastLastSuccessAt.Equal(now) {
		t.Fatalf("fast status missing: %+v", snapshot.Status)
	}
	upstream.err = errors.New("temporary fast failure")
	if err := server.CollectFastOnce(context.Background()); err == nil {
		t.Fatal("expected fast refresh error")
	}
	if after := server.Snapshot(); after.Version != snapshot.Version || after.Status.State != "ready" || len(after.Flips) != 1 {
		t.Fatalf("fast failure destroyed last good feed: %+v", after)
	}
}

func TestBroadPublishCannotReplaceNewerFastFeed(t *testing.T) {
	server, err := New(Config{}, &fakeUpstream{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC()
	newerFlip := Flip{Key: "newer", ItemID: "minecraft:diamond", ItemName: "Diamond", Quantity: 1,
		Price: 1, ReferenceValue: 2, Profit: 1, SearchCommand: "/ah diamond", SellerCommand: "/ah diamond", ItemCommand: "/ah diamond"}
	server.current.Store(&Snapshot{Version: 7, GeneratedAt: base.Add(time.Second), Status: Status{
		State: "ready", LastSuccessAt: base.Add(time.Second), FastLastSuccessAt: base.Add(time.Second),
		FastDurationMS: 500, FastListingsFetched: 44, ValuationCount: 9, FlipCount: 1,
	}, Flips: []Flip{newerFlip}})
	server.version.Store(7)

	server.publishBroadSnapshot(Snapshot{GeneratedAt: base, Status: Status{
		State: "ready", ListingsFetched: 9_680, TransactionsFetched: 1_000, ValuationCount: 20,
	}, Flips: []Flip{{Key: "stale"}}})

	snapshot := server.Snapshot()
	if snapshot.Version != 8 || len(snapshot.Flips) != 1 || snapshot.Flips[0].Key != "newer" {
		t.Fatalf("broad publish replaced a newer fast feed: %+v", snapshot)
	}
	if snapshot.Status.ListingsFetched != 9_680 || snapshot.Status.TransactionsFetched != 1_000 {
		t.Fatalf("broad counters were not published: %+v", snapshot.Status)
	}
	if snapshot.Status.FastListingsFetched != 44 || snapshot.Status.ValuationCount != 9 || snapshot.Status.FlipCount != 1 {
		t.Fatalf("newer fast status was not preserved: %+v", snapshot.Status)
	}
}

func TestBroadCollectionBuildsAwayFromLiveFastEngine(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	transactions := []market.Transaction{{
		SellerName: "seller", Item: market.Item{ID: "minecraft:diamond", Quantity: 1},
		TotalPrice: 1_000, SoldAt: now.Add(-time.Minute), Source: market.SourceDonutAPI,
	}}
	pageStarted := make(chan struct{})
	pageRelease := make(chan struct{})
	upstream := &fakeUpstream{pageStarted: pageStarted, pageRelease: pageRelease}
	server, err := New(Config{ListingPages: 1}, upstream, &memoryHistory{values: transactions}, nil)
	if err != nil {
		t.Fatal(err)
	}
	liveEngine := server.engine.Load()
	completed := make(chan error, 1)
	go func() { completed <- server.CollectOnce(context.Background()) }()
	<-pageStarted
	if during := server.engine.Load(); during != liveEngine {
		t.Fatal("broad collector replaced the live engine before its active-book merge completed")
	}
	close(pageRelease)
	if err := <-completed; err != nil {
		t.Fatal(err)
	}
	if after := server.engine.Load(); after == liveEngine {
		t.Fatal("completed broad model was not installed")
	}
}

func TestStartupFeedUsesJSONArray(t *testing.T) {
	server, err := New(Config{Address: "127.0.0.1:8080"}, &fakeUpstream{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/flips", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("startup feed code=%d", response.Code)
	}
	var payload struct {
		Flips json.RawMessage `json:"flips"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if string(payload.Flips) != "[]" {
		t.Fatalf("startup flips must be an array, got %s", payload.Flips)
	}
}

func TestOrderAuctionPageReportsRealObserverStateWithoutFakeRows(t *testing.T) {
	server, err := New(Config{Address: "127.0.0.1:8080"}, &fakeUpstream{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/order-auction-flipper", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("order-auction page code=%d", response.Code)
	}
	body := response.Body.String()
	for _, expected := range []string{"Order → Auction Flips", "Goal: keep 20 distinct profitable offers active.", "No current profitable offers.", "Retained profiles stay in the fast recheck rotation.", "/order-auction-flipper/debug"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("order-auction page missing %q", expected)
		}
	}
	for _, noise := range []string{"legacy reductions quarantined", "$10M", "Auction → existing order", "Blocked and stale candidate diagnostics"} {
		if strings.Contains(body, noise) {
			t.Fatalf("simple order page still contains debug section %q", noise)
		}
	}
	if strings.Contains(body, ">Refresh<") || !strings.Contains(body, "setInterval(update,1000)") {
		t.Fatal("simple order page is not using its one-second live update")
	}
	request = httptest.NewRequest(http.MethodGet, "/order-auction-flipper", nil)
	request.Header.Set("If-None-Match", response.Header().Get("ETag"))
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotModified || response.Body.Len() != 0 {
		t.Fatalf("unchanged live page code=%d bytes=%d", response.Code, response.Body.Len())
	}
	request = httptest.NewRequest(http.MethodGet, "/order-auction-flipper/debug", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Order → auction priority queue") || !strings.Contains(response.Body.String(), "No observer registered.") {
		t.Fatalf("order debug page code=%d body=%s", response.Code, response.Body.String())
	}
}

func TestFirstOrderBidSeparatesObservedBucketFromReservedCap(t *testing.T) {
	value := orders.Candidate{ObservedOrderUnitRewardCents: 130_000_000, OrderUnitRewardCents: 140_000_000}
	if got := firstOrderBidCents(value); got != 130_000_001 {
		t.Fatalf("first bid=%d", got)
	}
	value.ObservedOrderUnitRewardCents = value.OrderUnitRewardCents
	if got := firstOrderBidCents(value); got != 140_000_000 {
		t.Fatalf("exact first bid=%d", got)
	}
}

func TestScopedObserverAndFabricAPIs(t *testing.T) {
	server, err := New(Config{Address: "127.0.0.1:8080", ClientToken: "admin-token-123456", ObserverToken: "observer-token-123456", FabricToken: "fabric-token-123456"}, &fakeUpstream{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	register := `{"observer_id":"observer-1","parser_version":"p1","proxy_label":"proxy-1"}`
	response := requestJSON(server, http.MethodPost, "/api/v1/observers/register", "observer-token-123456", register)
	if response.Code != http.StatusOK {
		t.Fatalf("register code=%d body=%s", response.Code, response.Body.String())
	}
	response = requestJSON(server, http.MethodPost, "/api/v1/observers/register", "fabric-token-123456", register)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("fabric token reached observer API: %d", response.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/observers/tasks?observer_id=observer-1", nil)
	request.Header.Set("Authorization", "Bearer observer-token-123456")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"kind":"discovery"`) {
		t.Fatalf("task code=%d body=%s", response.Code, response.Body.String())
	}

	response = requestJSON(server, http.MethodPost, "/api/v1/client/diagnostics", "fabric-token-123456",
		`[{"install_id":"install-1","version":"1.0.0","event":"connection","fields":{"state":"ready"}}]`)
	if response.Code != http.StatusNoContent {
		t.Fatalf("diagnostic code=%d body=%s", response.Code, response.Body.String())
	}
	response = requestJSON(server, http.MethodPost, "/api/v1/client/diagnostics", "fabric-token-123456",
		`[{"install_id":"install-1","version":"1.0.0","event":"connection","fields":{"chat":"private"}}]`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("forbidden diagnostic accepted: %d", response.Code)
	}
}

func requestJSON(server *Server, method, path, token, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func TestFeedETagChangesWithFailureState(t *testing.T) {
	upstream := &fakeUpstream{}
	server, err := New(Config{Address: "127.0.0.1:8080"}, upstream, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	current := server.Snapshot()
	current.Version = 9
	current.Status.State = "ready"
	server.current.Store(&current)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/flips", nil)
	request.Header.Set("If-None-Match", "\"9-ready\"")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotModified {
		t.Fatalf("ready conditional code=%d", response.Code)
	}
	current.Status.State = "error"
	server.current.Store(&current)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("ETag") != "\"9-error\"" {
		t.Fatalf("error transition code=%d etag=%q", response.Code, response.Header().Get("ETag"))
	}
}

func TestHealthIsUnavailableBeforeFirstSuccessfulScan(t *testing.T) {
	server, err := New(Config{Address: "127.0.0.1:8080"}, &fakeUpstream{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("starting health code=%d", response.Code)
	}
	current := server.Snapshot()
	current.Status.State = "collecting"
	server.current.Store(&current)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("first collection health code=%d", response.Code)
	}
}

func TestValidateBindRequiresTokenOffLoopback(t *testing.T) {
	if err := ValidateBind("0.0.0.0:8080", ""); err == nil {
		t.Fatal("public bind without token accepted")
	}
	if err := ValidateBind("127.0.0.1:8080", ""); err != nil {
		t.Fatal(err)
	}
	if err := ValidateBind("0.0.0.0:8080", "long-random-token"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateBind("127.0.0.1:8080", "short"); err == nil {
		t.Fatal("weak configured token accepted")
	}
}

func TestValidateScopedTokensRequireDistinctRemoteCredentials(t *testing.T) {
	admin := "admin-token-123456"
	observer := "observer-token-123456"
	fabric := "fabric-token-123456"
	if err := ValidateScopedTokens("0.0.0.0:8080", admin, observer, fabric); err != nil {
		t.Fatal(err)
	}
	if err := ValidateScopedTokens("0.0.0.0:8080", admin, "", fabric); err == nil {
		t.Fatal("remote observer API accepted an empty credential")
	}
	if err := ValidateScopedTokens("0.0.0.0:8080", admin, admin, fabric); err == nil {
		t.Fatal("cross-scope credential reuse was accepted")
	}
}

func TestUnknownRouteIsNotDebugPage(t *testing.T) {
	server, err := New(Config{Address: "127.0.0.1:8080"}, &fakeUpstream{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/not-a-route", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown route code=%d", response.Code)
	}
}

func TestAuctionSearchCommandsAreCanonicalAndBounded(t *testing.T) {
	if got := itemSearchID("minecraft:redstone_block", "Redstone Block"); got != "redstone_block" {
		t.Fatalf("itemSearchID=%q", got)
	}
	if got := itemSearchID("", "Redstone Block\n/op me @a"); got != "redstone_block_op_me_a" {
		t.Fatalf("fallback itemSearchID=%q", got)
	}
	if got := sellerSearchCommand("Valid_Name1"); got != "/ah Valid_Name1" {
		t.Fatalf("sellerSearchCommand=%q", got)
	}
	if got := sellerSearchCommand("bad name"); got != "" {
		t.Fatalf("unsafe seller accepted: %q", got)
	}
}
