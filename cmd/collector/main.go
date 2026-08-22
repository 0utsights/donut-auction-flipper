package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"donut-network/internal/donutapi"
	"donut-network/internal/market"
)

const auctionPageSize = 44

var backendHTTP = &http.Client{Timeout: 10 * time.Second}

func main() {
	backend := flag.String("backend", env("DN_BACKEND_URL", "http://localhost:8080"), "backend URL")
	pages := flag.Int("listing-pages", 220, "maximum listing pages per full collection")
	interval := flag.Duration("interval", 60*time.Second, "collection interval")
	once := flag.Bool("once", false, "collect one cycle and exit")
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	apiKey := os.Getenv("DONUT_API_KEY")
	if apiKey == "" {
		logger.Error("DONUT_API_KEY is required for live collection")
		os.Exit(2)
	}
	client := donutapi.New(donutapi.Config{BaseURL: env("DONUT_API_BASE", "https://api.donutsmp.net"), APIKey: apiKey, RequestsPerMinute: 240, MaxRetries: 4})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var lastSuccess time.Time
	collect := func() (collectionErr error) {
		started := time.Now().UTC()
		status := market.CollectorStatus{State: "collecting", CycleStartedAt: started, NextCollectionAt: started.Add(*interval)}
		_ = post(ctx, *backend+"/api/v1/ingest/status", status)
		defer func() {
			apiStats := client.Stats()
			status.CycleCompletedAt = time.Now().UTC()
			status.CycleDurationMS = float64(time.Since(started)) / float64(time.Millisecond)
			status.APIRequests = apiStats.Requests
			status.APIErrors = apiStats.Errors
			status.Retries = apiStats.Retries
			status.RateLimitResponses = apiStats.RateLimitResponses
			status.LastAPILatencyMS = apiStats.LastLatencyMS
			if collectionErr != nil {
				status.State = "error"
				status.LastSuccessAt = lastSuccess
				status.Message = safeMessage(collectionErr)
			} else {
				status.State = "ready"
				lastSuccess = status.CycleCompletedAt
				status.LastSuccessAt = lastSuccess
			}
			if err := post(ctx, *backend+"/api/v1/ingest/status", status); err != nil {
				logger.Warn("could not publish collector status", "error", err)
			}
		}()
		transactions, err := client.AllTransactionPages(ctx)
		if err != nil {
			return fmt.Errorf("transactions: %w", err)
		}
		status.TransactionsFetched = len(transactions)
		if err := post(ctx, *backend+"/api/v1/ingest/transactions", transactions); err != nil {
			return err
		}
		for p := 1; p <= *pages; p++ {
			listings, err := client.AuctionPage(ctx, p, "", "recently_listed")
			if err != nil {
				return fmt.Errorf("listings page %d: %w", p, err)
			}
			status.ListingsFetched += len(listings)
			if len(listings) > 0 {
				if err := post(ctx, *backend+"/api/v1/ingest/listings", listings); err != nil {
					return err
				}
			}
			// Auction pages are padded to 44 rows upstream. The API mapper removes
			// null padding, so a short page is the authoritative end of the book.
			if len(listings) < auctionPageSize {
				break
			}
			if p == *pages {
				status.Message = "listing scan reached configured page cap"
			}
		}
		logger.Info("collection complete", "transactions", len(transactions), "listings", status.ListingsFetched, "duration", time.Since(started))
		return nil
	}
	if *pages < 1 || *pages > 220 {
		logger.Error("listing-pages must be between 1 and 220")
		os.Exit(2)
	}
	if err := collect(); err != nil {
		logger.Error("collection failed", "error", err)
		if *once {
			os.Exit(1)
		}
	}
	if *once {
		return
	}
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := collect(); err != nil {
				logger.Error("collection failed", "error", err)
			}
		}
	}
}
func post(ctx context.Context, url string, value any) error {
	b, _ := json.Marshal(value)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+env("DN_COLLECTOR_TOKEN", "local-collector-token"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Client-ID", "official-collector")
	req.Header.Set("X-Data-Mode", "live")
	resp, err := backendHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("backend %s returned %s", url, resp.Status)
	}
	return nil
}

func safeMessage(err error) string {
	message := strings.Join(strings.Fields(err.Error()), " ")
	if len(message) > 500 {
		message = message[:500]
	}
	return message
}
func env(k, f string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return f
}
