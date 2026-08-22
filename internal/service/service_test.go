package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"donut-network/internal/donutapi"
	"donut-network/internal/market"
)

type fakeUpstream struct {
	transactions []market.Transaction
	listings     []market.Listing
	err          error
	pages        int
}

func (f *fakeUpstream) AllTransactionPages(context.Context) ([]market.Transaction, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.transactions, nil
}
func (f *fakeUpstream) AuctionPage(context.Context, int, string, string) ([]market.Listing, error) {
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
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	item := market.Item{ID: "minecraft:diamond", Quantity: 1, DisplayName: "Diamond"}
	transactions := make([]market.Transaction, 0, 12)
	for index := 0; index < 12; index++ {
		transactions = append(transactions, market.Transaction{SellerName: "seller", Item: item, TotalPrice: 1_000_000 + int64(index*1_000), SoldAt: now.Add(-time.Duration(index+1) * time.Hour), Source: market.SourceDonutAPI})
	}
	listing := market.NormalizeListing(market.Listing{AuthoritativeID: "auction-1", SellerName: "cheap", Item: item, TotalPrice: 500_000, LastSeen: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour), Source: market.SourceDonutAPI})
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
	if snapshot.Flips[0].SearchCommand != "/ah Diamond" {
		t.Fatalf("unsafe/wrong search command: %q", snapshot.Flips[0].SearchCommand)
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

func TestSafeSearchRemovesCommands(t *testing.T) {
	if got := safeSearch("Diamond\n/op me @a"); got != "Diamond op me a" {
		t.Fatalf("safeSearch=%q", got)
	}
}
