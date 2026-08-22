package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"donut-network/internal/market"
)

func main() {
	backend := flag.String("backend", "http://localhost:8080", "backend URL")
	clients := flag.Int("clients", 100, "concurrent clients")
	events := flag.Int("events", 100, "observations per client")
	ramp := flag.Duration("ramp", 0, "spread client connection starts over this duration")
	flag.Parse()
	token := env("DN_AUTH_TOKEN", "local-worker-token")
	var failed atomic.Uint64
	failureReasons := map[string]int{}
	durations := make([]time.Duration, 0, *clients**events)
	var mu sync.Mutex
	start := time.Now()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = *clients
	transport.MaxIdleConnsPerHost = *clients
	transport.MaxConnsPerHost = *clients
	httpClient := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	var wg sync.WaitGroup
	for c := 0; c < *clients; c++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if *ramp > 0 {
				timer := time.NewTimer(time.Duration(id) * *ramp / time.Duration(max(1, *clients)))
				<-timer.C
			}
			local := make([]time.Duration, 0, *events)
			for i := 0; i < *events; i++ {
				listing := market.NormalizeListing(market.Listing{SellerName: fmt.Sprintf("load-%d", id), Item: market.Item{ID: "minecraft:elytra", Quantity: 1}, TotalPrice: 250_000_000 + int64(i), Source: market.SourceClient})
				body, _ := json.Marshal(listing)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				req, _ := http.NewRequestWithContext(ctx, http.MethodPost, *backend+"/api/v1/observations", bytes.NewReader(body))
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Client-ID", fmt.Sprintf("load-%d", id))
				t0 := time.Now()
				resp, err := httpClient.Do(req)
				if err != nil || resp.StatusCode >= 300 {
					failed.Add(1)
					reason := "request error"
					if err != nil {
						reason = err.Error()
					} else {
						reason = resp.Status
					}
					mu.Lock()
					failureReasons[reason]++
					mu.Unlock()
				}
				if resp != nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
				local = append(local, time.Since(t0))
				cancel()
			}
			mu.Lock()
			durations = append(durations, local...)
			mu.Unlock()
		}(c)
	}
	wg.Wait()
	elapsed := time.Since(start)
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	total := len(durations)
	fmt.Printf("clients=%d events=%d duration=%s throughput=%.1f events/s failed=%d\n", *clients, total, elapsed.Round(time.Millisecond), float64(total)/elapsed.Seconds(), failed.Load())
	if total > 0 {
		fmt.Printf("latency p50=%s p95=%s p99=%s max=%s\n", pct(durations, .50), pct(durations, .95), pct(durations, .99), durations[total-1])
	}
	for reason, count := range failureReasons {
		fmt.Printf("failure %d: %s\n", count, reason)
	}
}
func pct(v []time.Duration, p float64) time.Duration {
	idx := int(float64(len(v)-1) * p)
	return v[idx]
}
func env(k, f string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return f
}
