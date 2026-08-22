package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"donut-network/infra/migrations"
	"donut-network/internal/market"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Postgres struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, dsn string) (*Postgres, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 16
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 30 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Postgres{pool: pool}, nil
}
func (p *Postgres) Close() { p.pool.Close() }

func (p *Postgres) Migrate(ctx context.Context) error {
	connection, err := p.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer connection.Release()
	if _, err = connection.Exec(ctx, `SELECT pg_advisory_lock(641190247)`); err != nil {
		return err
	}
	defer connection.Exec(context.Background(), `SELECT pg_advisory_unlock(641190247)`)
	if _, err = connection.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(name text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	entries, err := migrations.Files.ReadDir(".")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		var applied bool
		if err = connection.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name=$1)`, name).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		sql, readErr := migrations.Files.ReadFile(name)
		if readErr != nil {
			return readErr
		}
		if _, err = connection.Conn().PgConn().Exec(ctx, string(sql)).ReadAll(); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err = connection.Exec(ctx, `INSERT INTO schema_migrations(name) VALUES($1)`, name); err != nil {
			return err
		}
	}
	return nil
}

func (p *Postgres) StoreTransactions(ctx context.Context, items []market.Transaction) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	batch := &pgx.Batch{}
	for _, t := range items {
		raw, _ := json.Marshal(t.Item)
		batch.Queue(`INSERT INTO item_signatures(exact_signature,base_signature,modifiers) VALUES($1,$2,$3) ON CONFLICT(exact_signature) DO UPDATE SET last_seen_at=now()`, t.Signature.Exact, t.Signature.Base, t.Signature.Modifiers)
		batch.Queue(`INSERT INTO transactions(fingerprint,item_signature,seller_uuid,seller_name,total_price,unit_price,quantity,sold_at,source,item_data) VALUES($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(fingerprint,sold_at) DO NOTHING`, t.Fingerprint, t.Signature.Exact, t.SellerUUID, t.SellerName, t.TotalPrice, t.UnitPrice, t.Item.Quantity, t.SoldAt, t.Source, raw)
	}
	results := tx.SendBatch(ctx, batch)
	for range items {
		if _, err = results.Exec(); err != nil {
			_ = results.Close()
			return err
		}
		if _, err = results.Exec(); err != nil {
			_ = results.Close()
			return err
		}
	}
	if err = results.Close(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (p *Postgres) UpsertListings(ctx context.Context, items []market.Listing) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	batch := &pgx.Batch{}
	for _, l := range items {
		raw, _ := json.Marshal(l.Item)
		batch.Queue(`INSERT INTO item_signatures(exact_signature,base_signature,modifiers) VALUES($1,$2,$3) ON CONFLICT(exact_signature) DO UPDATE SET last_seen_at=now()`, l.Signature.Exact, l.Signature.Base, l.Signature.Modifiers)
		batch.Queue(`INSERT INTO auction_listings(fingerprint,authoritative_id,item_signature,seller_uuid,seller_name,total_price,unit_price,quantity,first_seen,last_seen,expires_at,source,search_context,page,item_data,active) VALUES($1,NULLIF($2,''),$3,NULLIF($4,''),$5,$6,$7,$8,$9,$10,NULLIF($11,'0001-01-01 00:00:00+00'::timestamptz),$12,NULLIF($13,''),NULLIF($14,0),$15,true) ON CONFLICT(fingerprint) DO UPDATE SET last_seen=EXCLUDED.last_seen, expires_at=COALESCE(EXCLUDED.expires_at,auction_listings.expires_at), observer_count=auction_listings.observer_count+1, active=true`, l.Fingerprint, l.AuthoritativeID, l.Signature.Exact, l.SellerUUID, l.SellerName, l.TotalPrice, l.UnitPrice, l.Item.Quantity, l.FirstSeen, l.LastSeen, l.ExpiresAt, l.Source, l.SearchContext, l.Page, raw)
	}
	results := tx.SendBatch(ctx, batch)
	for range items {
		if _, err = results.Exec(); err != nil {
			_ = results.Close()
			return err
		}
		if _, err = results.Exec(); err != nil {
			_ = results.Close()
			return err
		}
	}
	if err = results.Close(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) StoreValuations(ctx context.Context, valuations []market.Valuation) error {
	if len(valuations) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, valuation := range valuations {
		riskFlags, _ := json.Marshal(valuation.RiskFlags)
		batch.Queue(`INSERT INTO valuations(item_signature,generated_at,fair_value,quick_sell_value,short_term_value,long_term_value,active_best_ask,active_reference_ask,active_depth,active_seller_count,confidence_bps,sample_count,raw_sample_count,seller_count,fresh_sample_count,volume_24h,volatility_bps,spread_bps,expected_sell_minutes,reference_age_seconds,short_window_hours,regime,risk_flags,model_version,fallback_level) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,0),NULLIF($8,0),$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25) ON CONFLICT(item_signature,generated_at) DO NOTHING`, valuation.Signature, valuation.GeneratedAt, valuation.FairValue, valuation.QuickSellValue, valuation.ShortTermValue, valuation.LongTermValue, valuation.ActiveBestAsk, valuation.ActiveReferenceAsk, valuation.ActiveDepth, valuation.ActiveSellerCount, valuation.ConfidenceBPS, valuation.SampleCount, valuation.RawSampleCount, valuation.SellerCount, valuation.FreshSampleCount, valuation.Volume24h, valuation.VolatilityBPS, valuation.SpreadBPS, valuation.ExpectedSellMinutes, valuation.ReferenceAgeSeconds, valuation.ShortWindowHours, valuation.Regime, riskFlags, valuation.ModelVersion, valuation.FallbackLevel)
	}
	results := p.pool.SendBatch(ctx, batch)
	for range valuations {
		if _, err := results.Exec(); err != nil {
			_ = results.Close()
			return err
		}
	}
	return results.Close()
}

func (p *Postgres) StoreCollectorStatus(ctx context.Context, status market.CollectorStatus) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO collector_cycles(cycle_started_at,state,cycle_completed_at,last_success_at,next_collection_at,listings_fetched,transactions_fetched,api_requests,api_errors,retries,rate_limit_responses,last_api_latency_ms,cycle_duration_ms,message) VALUES($1,$2,NULLIF($3,'0001-01-01 00:00:00+00'::timestamptz),NULLIF($4,'0001-01-01 00:00:00+00'::timestamptz),NULLIF($5,'0001-01-01 00:00:00+00'::timestamptz),$6,$7,$8,$9,$10,$11,$12,$13,NULLIF($14,'')) ON CONFLICT(cycle_started_at) DO UPDATE SET state=EXCLUDED.state,cycle_completed_at=EXCLUDED.cycle_completed_at,last_success_at=EXCLUDED.last_success_at,next_collection_at=EXCLUDED.next_collection_at,listings_fetched=EXCLUDED.listings_fetched,transactions_fetched=EXCLUDED.transactions_fetched,api_requests=EXCLUDED.api_requests,api_errors=EXCLUDED.api_errors,retries=EXCLUDED.retries,rate_limit_responses=EXCLUDED.rate_limit_responses,last_api_latency_ms=EXCLUDED.last_api_latency_ms,cycle_duration_ms=EXCLUDED.cycle_duration_ms,message=EXCLUDED.message`, status.CycleStartedAt, status.State, status.CycleCompletedAt, status.LastSuccessAt, status.NextCollectionAt, status.ListingsFetched, status.TransactionsFetched, status.APIRequests, status.APIErrors, status.Retries, status.RateLimitResponses, status.LastAPILatencyMS, status.CycleDurationMS, status.Message)
	return err
}
func (p *Postgres) LoadRecentTransactions(ctx context.Context, since time.Time, limit int) ([]market.Transaction, error) {
	rows, err := p.pool.Query(ctx, `SELECT fingerprint,seller_uuid,seller_name,total_price,unit_price,sold_at,source,item_data FROM transactions WHERE sold_at >= $1 ORDER BY sold_at DESC LIMIT $2`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []market.Transaction{}
	for rows.Next() {
		var t market.Transaction
		var raw []byte
		if err := rows.Scan(&t.Fingerprint, &t.SellerUUID, &t.SellerName, &t.TotalPrice, &t.UnitPrice, &t.SoldAt, &t.Source, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &t.Item); err != nil {
			return nil, fmt.Errorf("decode item: %w", err)
		}
		t = market.NormalizeTransaction(t)
		out = append(out, t)
	}
	return out, rows.Err()
}
