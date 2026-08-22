package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"donut-network/internal/persistence"
	"donut-network/internal/platform"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	addr := env("DN_HTTP_ADDR", "127.0.0.1:8080")
	var repository platform.Repository
	var database *persistence.Postgres
	if dsn := os.Getenv("DN_DATABASE_URL"); dsn != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		var err error
		database, err = persistence.Open(ctx, dsn)
		if err != nil {
			cancel()
			logger.Error("database connection failed", "error", err)
			os.Exit(1)
		}
		if err = database.Migrate(ctx); err != nil {
			cancel()
			logger.Error("database migration failed", "error", err)
			database.Close()
			os.Exit(1)
		}
		cancel()
		repository = database
		defer database.Close()
	}
	dataMode := env("DN_DATA_MODE", "live")
	server := platform.NewServer(platform.Config{WorkerToken: env("DN_AUTH_TOKEN", "local-worker-token"), CollectorToken: env("DN_COLLECTOR_TOKEN", "local-collector-token"), AdminToken: env("DN_ADMIN_TOKEN", "local-admin-token"), DataMode: dataMode, AllowedOrigins: []string{"https://donut-network.example"}, Logger: logger, Repository: repository})
	if database != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		transactions, err := database.LoadRecentTransactions(ctx, time.Now().Add(-30*24*time.Hour), 100_000)
		cancel()
		if err != nil {
			logger.Error("load market history", "error", err)
			os.Exit(1)
		}
		if dataMode == "live" {
			live := transactions[:0]
			for _, transaction := range transactions {
				if transaction.Source != "simulator" {
					live = append(live, transaction)
				}
			}
			if removed := len(transactions) - len(live); removed > 0 {
				logger.Warn("ignored persisted simulated history in live mode", "transactions", removed)
			}
			transactions = live
		}
		server.Engine().AddTransactions(transactions)
		logger.Info("restored market history", "transactions", len(transactions))
	}
	httpServer := &http.Server{Addr: addr, Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() {
		logger.Info("backend listening", "address", addr, "data_mode", dataMode, "purchase_mode", "simulation-safe")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server stopped", "error", err)
			os.Exit(1)
		}
	}()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
	logger.Info("backend shutdown complete")
}
func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
