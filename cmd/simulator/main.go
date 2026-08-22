package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"time"

	"donut-network/internal/market"
	"donut-network/internal/network"
	"donut-network/internal/worker"
	"github.com/gorilla/websocket"
)

type itemProfile struct {
	ID       string
	Base     int64
	Enchants map[string]int
}

var catalog = []itemProfile{{"minecraft:elytra", 300_000_000, nil}, {"minecraft:netherite_sword", 82_000_000, map[string]int{"minecraft:sharpness": 5, "minecraft:unbreaking": 3, "minecraft:mending": 1}}, {"minecraft:spawner", 20_000_000, nil}, {"minecraft:mace", 410_000_000, map[string]int{"minecraft:density": 5}}, {"minecraft:shulker_box", 8_500_000, nil}, {"minecraft:totem_of_undying", 2_200_000, nil}}

type simWorker struct {
	id                         string
	cache                      *worker.Cache
	purchase                   worker.SimulatorPurchaseController
	conn                       *websocket.Conn
	sendMu                     sync.Mutex
	observed, flips, purchases atomic.Uint64
}

func main() {
	backend := flag.String("backend", env("DN_BACKEND_URL", "http://localhost:8080"), "backend URL")
	rate := flag.Int("rate", 20, "listings per second")
	workerCount := flag.Int("workers", 8, "simulated worker count")
	flipPercent := flag.Int("flip-percent", 8, "underpriced listing percentage")
	seedCount := flag.Int("history", 80, "transactions seeded per signature")
	flag.Parse()
	workerToken := env("DN_AUTH_TOKEN", "local-worker-token")
	ingestToken := env("DN_COLLECTOR_TOKEN", "local-collector-token")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := seed(ctx, *backend, ingestToken, *seedCount); err != nil {
		log.Fatalf("seed market: %v", err)
	}
	workers := make([]*simWorker, 0, *workerCount)
	for i := 0; i < *workerCount; i++ {
		w, err := connectWorker(ctx, *backend, workerToken, i)
		if err != nil {
			log.Fatalf("connect worker %d: %v", i, err)
		}
		workers = append(workers, w)
	}
	log.Printf("simulator running: rate=%d/s workers=%d flip_frequency=%d%%", *rate, *workerCount, *flipPercent)
	interval := time.Second / time.Duration(max(1, *rate))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	flushTicker := time.NewTicker(500 * time.Millisecond)
	defer flushTicker.Stop()
	started := time.Now()
	generated := uint64(0)
	pending := make([]market.Listing, 0, max(1, *rate/2))
	for {
		select {
		case <-ctx.Done():
			for _, w := range workers {
				_ = w.conn.Close()
			}
			printStats(workers, generated, time.Since(started))
			return
		case now := <-ticker.C:
			l := randomListing(now, *flipPercent)
			generated++
			pending = append(pending, l)
			w := workers[int(generated)%len(workers)]
			w.observed.Add(1)
			go w.evaluate(l)
		case <-flushTicker.C:
			if len(pending) > 0 {
				batch := append([]market.Listing(nil), pending...)
				pending = pending[:0]
				postJSONAsync(ctx, *backend+"/api/v1/ingest/listings", ingestToken, "simulator-market", batch)
			}
		}
	}
}

func seed(ctx context.Context, backend, token string, count int) error {
	transactions := make([]market.Transaction, 0, count*len(catalog))
	now := time.Now().UTC()
	for _, p := range catalog {
		for i := 0; i < count; i++ {
			noise := int64(rand.IntN(1201) - 600)
			price := p.Base * (10_000 + noise) / 10_000
			t := market.Transaction{SellerName: fmt.Sprintf("seller-%d", rand.IntN(500)), Item: market.Item{ID: p.ID, Quantity: 1, Enchantments: p.Enchants}, TotalPrice: price, SoldAt: now.Add(-time.Duration(rand.IntN(14*24)) * time.Hour), Source: market.SourceSimulator}
			transactions = append(transactions, market.NormalizeTransaction(t))
		}
	}
	return postJSON(ctx, backend+"/api/v1/ingest/transactions", token, "simulator-seed", transactions)
}
func randomListing(now time.Time, flipPercent int) market.Listing {
	p := catalog[rand.IntN(len(catalog))]
	quantity := 1
	if p.ID == "minecraft:totem_of_undying" {
		quantity = 1 + rand.IntN(16)
	}
	// Rotate bull/normal/bear regimes every 20 seconds and occasionally inject a manipulated outlier.
	regimes := [...]int{9_000, 10_000, 11_200}
	bps := regimes[int(now.Unix()/20)%len(regimes)] + rand.IntN(1801) - 900
	if rand.IntN(100) < flipPercent {
		bps = 6500 + rand.IntN(1800)
	} else if rand.IntN(100) == 0 {
		bps *= 5
	}
	unit := p.Base * int64(bps) / 10_000
	l := market.Listing{SellerName: fmt.Sprintf("seller-%d", rand.IntN(800)), Item: market.Item{ID: p.ID, Quantity: quantity, Enchantments: p.Enchants}, TotalPrice: unit * int64(quantity), FirstSeen: now, LastSeen: now, ExpiresAt: now.Add(30 * time.Second), Source: market.SourceSimulator, SearchContext: p.ID}
	return market.NormalizeListing(l)
}
func connectWorker(ctx context.Context, backend, token string, index int) (*simWorker, error) {
	u, err := url.Parse(backend)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = "/ws"
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	header.Set("X-Data-Mode", "simulation")
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), header)
	if err != nil {
		return nil, err
	}
	w := &simWorker{id: fmt.Sprintf("sim-worker-%02d", index+1), cache: worker.NewCache(2 * time.Minute), purchase: worker.SimulatorPurchaseController{SuccessRatePercent: 72}, conn: conn}
	go w.readLoop()
	go w.heartbeat(ctx, index)
	return w, nil
}
func (w *simWorker) readLoop() {
	for {
		kind, data, err := w.conn.ReadMessage()
		if err != nil {
			return
		}
		if kind != websocket.BinaryMessage {
			continue
		}
		frame, err := network.Decode(data)
		if err != nil {
			continue
		}
		if frame.Type == network.MsgSnapshot {
			var s market.Snapshot
			if json.Unmarshal(frame.Payload, &s) == nil {
				w.cache.Replace(s)
			}
		} else if frame.Type == network.MsgPriceUpdate {
			var update market.PriceUpdate
			if json.Unmarshal(frame.Payload, &update) == nil {
				w.cache.Apply(update)
			}
		} else if frame.Type == network.MsgPriceInvalidation {
			var invalidation struct {
				Version     uint64    `json:"version"`
				GeneratedAt time.Time `json:"generated_at"`
				Signature   string    `json:"signature"`
			}
			if json.Unmarshal(frame.Payload, &invalidation) == nil {
				w.cache.Invalidate(invalidation.Version, invalidation.GeneratedAt, invalidation.Signature)
			}
		}
	}
}
func (w *simWorker) heartbeat(ctx context.Context, index int) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	send := func() {
		hb := network.WorkerState{WorkerID: w.id, Username: w.id, Online: true, PingMS: 18 + index*4, AvailableBalance: 1_000_000_000, InventoryCapacity: 20, SuccessRateBPS: 6500 + index*200, Region: "local", Capabilities: []string{"observe", "simulate_purchase"}}
		w.send(network.P1, network.MsgWorkerHeartbeat, hb)
	}
	send()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			send()
		}
	}
}
func (w *simWorker) evaluate(l market.Listing) {
	start := time.Now()
	opportunity, ok := w.cache.Evaluate(l, market.Thresholds{MinProfit: 1_000_000, MinMarginBPS: 400, MinConfidenceBPS: 4500, MaxPurchasePrice: 900_000_000, MinVolume24h: 1})
	observation := l
	w.send(network.P1, network.MsgListingObserved, observation)
	if !ok {
		return
	}
	w.flips.Add(1)
	w.send(network.P1, network.MsgFlipDetected, network.TelemetryEvent{ClientID: w.id, Kind: "FLIP_DETECTED", Fingerprint: l.Fingerprint, Signature: l.Signature.Exact, Price: l.TotalPrice, LatencyNS: opportunity.DecisionNS, ObservedAt: time.Now().UnixMilli(), Metadata: map[string]string{"profit": fmt.Sprint(opportunity.Profit)}})
	w.send(network.P1, network.MsgPurchaseResult, network.TelemetryEvent{ClientID: w.id, Kind: "PURCHASE_ATTEMPT", Fingerprint: l.Fingerprint, Signature: l.Signature.Exact, Price: l.TotalPrice, ObservedAt: time.Now().UnixMilli()})
	result, _ := w.purchase.Attempt(worker.PurchaseRequest{Listing: l, ExpectedSignature: l.Signature.Exact, ExpectedPrice: l.TotalPrice, ExpectedSeller: l.SellerName})
	if result.Success {
		w.purchases.Add(1)
	}
	w.send(network.P1, network.MsgPurchaseResult, network.TelemetryEvent{ClientID: w.id, Kind: map[bool]string{true: "PURCHASE_SUCCESS", false: "PURCHASE_FAILED"}[result.Success], Fingerprint: l.Fingerprint, Signature: l.Signature.Exact, Price: l.TotalPrice, LatencyNS: time.Since(start).Nanoseconds(), Success: result.Success, ObservedAt: time.Now().UnixMilli(), Metadata: map[string]string{"reason": result.Reason}})
}
func (w *simWorker) send(p network.Priority, t network.MessageType, v any) {
	b, err := network.Encode(p, t, v)
	if err != nil {
		return
	}
	w.sendMu.Lock()
	defer w.sendMu.Unlock()
	_ = w.conn.SetWriteDeadline(time.Now().Add(time.Second))
	_ = w.conn.WriteMessage(websocket.BinaryMessage, b)
}
func postJSONAsync(ctx context.Context, url, token, clientID string, v any) {
	go func() {
		if err := postJSON(ctx, url, token, clientID, v); err != nil && ctx.Err() == nil {
			log.Printf("market ingest failed: %v", err)
		}
	}()
}
func postJSON(ctx context.Context, url, token, clientID string, v any) error {
	b, _ := json.Marshal(v)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Client-ID", clientID)
	req.Header.Set("X-Data-Mode", "simulation")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s returned %s", url, resp.Status)
	}
	return nil
}
func printStats(workers []*simWorker, generated uint64, d time.Duration) {
	var observed, flips, purchases uint64
	for _, w := range workers {
		observed += w.observed.Load()
		flips += w.flips.Load()
		purchases += w.purchases.Load()
	}
	log.Printf("summary duration=%s generated=%d observed=%d flips=%d purchases=%d throughput=%.1f/s", d.Round(time.Millisecond), generated, observed, flips, purchases, float64(generated)/d.Seconds())
}
func env(k, f string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return f
}
