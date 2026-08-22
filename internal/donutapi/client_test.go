package donutapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestAuctionPageMapsOfficialSchema(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/auction/list/2" {
			t.Errorf("path=%s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":200,"result":[null,{"item":{"id":"minecraft:netherite_sword","count":1,"display_name":"Sword","lore":["test"],"enchants":{"enchantments":{"levels":{"minecraft:sharpness":5}},"trim":{"material":"gold","pattern":"spire"}},"contents":[{"id":"minecraft:diamond","count":3,"display_name":"","enchants":{}}]},"price":123456789,"seller":{"name":"alex","uuid":"u1"},"time_left":99000}]}`))
	}))
	defer srv.Close()
	c := NewWithHTTP(Config{BaseURL: srv.URL, APIKey: "secret", RequestsPerMinute: 250}, srv.Client())
	ls, err := c.AuctionPage(context.Background(), 2, "sword", "recently_listed")
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer secret" {
		t.Fatalf("auth=%q", auth)
	}
	if len(ls) != 1 || ls[0].UnitPrice != 123456789 || ls[0].Signature.Exact != "minecraft:netherite_sword|minecraft:sharpness=5;trim=spire:gold;name=sword;contents=3xminecraft:diamond" {
		t.Fatalf("bad mapping: %+v", ls)
	}
	if len(ls[0].Item.Contents) != 1 || time.Until(ls[0].ExpiresAt) < 90*time.Second {
		t.Fatalf("container contents or expiry missing: %+v", ls[0])
	}
}
func TestRetriesServerErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			http.Error(w, "retry", 500)
			return
		}
		_, _ = w.Write([]byte(`{"status":200,"result":[]}`))
	}))
	defer srv.Close()
	c := NewWithHTTP(Config{BaseURL: srv.URL, RequestsPerMinute: 250, MaxRetries: 3, Timeout: time.Second}, srv.Client())
	_, err := c.TransactionPage(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls=%d", calls.Load())
	}
	stats := c.Stats()
	if stats.Requests != 3 || stats.Retries != 2 || stats.Errors != 2 || stats.LastSuccessAt.IsZero() {
		t.Fatalf("stats=%+v", stats)
	}
}
func TestTransactionPageBounds(t *testing.T) {
	c := New(Config{})
	if _, err := c.TransactionPage(context.Background(), 11); err == nil {
		t.Fatal("page 11 accepted")
	}
}

func TestRejectsTrailingJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":200,"result":[]} {"unexpected":true}`))
	}))
	defer srv.Close()
	client := NewWithHTTP(Config{BaseURL: srv.URL, RequestsPerMinute: 250}, srv.Client())
	if _, err := client.AuctionPage(context.Background(), 1, "", ""); err == nil {
		t.Fatal("trailing JSON was accepted")
	}
}
