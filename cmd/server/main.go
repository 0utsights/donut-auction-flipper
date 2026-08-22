package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"donut-network/internal/donutapi"
	"donut-network/internal/market"
	"donut-network/internal/service"
	"donut-network/internal/state"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(logger); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	apiKey := strings.TrimSpace(os.Getenv("DONUT_API_KEY"))
	if apiKey == "" {
		return errors.New("DONUT_API_KEY is required")
	}
	address := env("DN_ADDRESS", "127.0.0.1:8080")
	clientToken := strings.TrimSpace(os.Getenv("DN_CLIENT_TOKEN"))
	if err := service.ValidateBind(address, clientToken); err != nil {
		return err
	}
	listingPages, err := envInt("DN_LISTING_PAGES", 220, 1, 220)
	if err != nil {
		return err
	}
	pause, err := envDuration("DN_COLLECTION_PAUSE", 5*time.Second, time.Second, 10*time.Minute)
	if err != nil {
		return err
	}
	thresholds := market.Thresholds{}
	if thresholds.MinProfit, err = envInt64("DN_MIN_PROFIT", 100_000, 1, 9_000_000_000_000_000_000); err != nil {
		return err
	}
	if thresholds.MinMarginBPS, err = envInt("DN_MIN_MARGIN_BPS", 1_000, 1, 1_000_000); err != nil {
		return err
	}
	if thresholds.MinConfidenceBPS, err = envInt("DN_MIN_CONFIDENCE_BPS", 5_000, 1, 10_000); err != nil {
		return err
	}
	if thresholds.MinVolume24h, err = envInt("DN_MIN_VOLUME_24H", 2, 1, 1_000_000); err != nil {
		return err
	}
	if thresholds.MaxPurchasePrice, err = envInt64("DN_MAX_PURCHASE_PRICE", 0, 0, 9_000_000_000_000_000_000); err != nil {
		return err
	}

	upstream := donutapi.New(donutapi.Config{
		BaseURL: env("DONUT_API_BASE", "https://api.donutsmp.net"), APIKey: apiKey,
		RequestsPerMinute: 240, MaxRetries: 4, Timeout: 10 * time.Second,
	})
	history := state.NewFile(env("DN_HISTORY_FILE", "data/history.json.gz"), 31*24*time.Hour, 100_000)
	application, err := service.New(service.Config{
		Address: address, ClientToken: clientToken, ListingPages: listingPages, CollectionPause: pause,
		OpportunityLimit: 100, Thresholds: thresholds,
	}, upstream, history, logger)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	httpServer := application.HTTPServer()
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("debug and flip server listening", "address", address, "client_auth", clientToken != "")
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()
	go application.RunCollector(ctx)

	select {
	case <-ctx.Done():
	case err := <-serverErrors:
		stop()
		return fmt.Errorf("listen: %w", err)
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdownContext)
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback, minimum, maximum int) (int, error) {
	value, err := envInt64(key, int64(fallback), int64(minimum), int64(maximum))
	return int(value), err
}

func envInt64(key string, fallback, minimum, maximum int64) (int64, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d", key, minimum, maximum)
	}
	return value, nil
}

func envDuration(key string, fallback, minimum, maximum time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be between %s and %s", key, minimum, maximum)
	}
	return value, nil
}
