BEGIN;

ALTER TABLE valuations ADD COLUMN IF NOT EXISTS short_term_value bigint NOT NULL DEFAULT 0;
ALTER TABLE valuations ADD COLUMN IF NOT EXISTS long_term_value bigint NOT NULL DEFAULT 0;
ALTER TABLE valuations ADD COLUMN IF NOT EXISTS active_reference_ask bigint;
ALTER TABLE valuations ADD COLUMN IF NOT EXISTS active_depth integer NOT NULL DEFAULT 0;
ALTER TABLE valuations ADD COLUMN IF NOT EXISTS active_seller_count integer NOT NULL DEFAULT 0;
ALTER TABLE valuations ADD COLUMN IF NOT EXISTS raw_sample_count integer NOT NULL DEFAULT 0;
ALTER TABLE valuations ADD COLUMN IF NOT EXISTS seller_count integer NOT NULL DEFAULT 0;
ALTER TABLE valuations ADD COLUMN IF NOT EXISTS fresh_sample_count integer NOT NULL DEFAULT 0;
ALTER TABLE valuations ADD COLUMN IF NOT EXISTS spread_bps integer NOT NULL DEFAULT 0;
ALTER TABLE valuations ADD COLUMN IF NOT EXISTS expected_sell_minutes integer NOT NULL DEFAULT 0;
ALTER TABLE valuations ADD COLUMN IF NOT EXISTS reference_age_seconds bigint NOT NULL DEFAULT 0;
ALTER TABLE valuations ADD COLUMN IF NOT EXISTS short_window_hours integer NOT NULL DEFAULT 0;
ALTER TABLE valuations ADD COLUMN IF NOT EXISTS regime text NOT NULL DEFAULT 'stable';
ALTER TABLE valuations ADD COLUMN IF NOT EXISTS risk_flags jsonb NOT NULL DEFAULT '[]';
ALTER TABLE valuations ADD COLUMN IF NOT EXISTS fallback_level text NOT NULL DEFAULT 'exact';

CREATE TABLE IF NOT EXISTS collector_cycles (
  cycle_started_at timestamptz PRIMARY KEY,
  state text NOT NULL CHECK (state IN ('collecting','ready','error')),
  cycle_completed_at timestamptz,
  last_success_at timestamptz,
  next_collection_at timestamptz,
  listings_fetched integer NOT NULL DEFAULT 0,
  transactions_fetched integer NOT NULL DEFAULT 0,
  api_requests bigint NOT NULL DEFAULT 0,
  api_errors bigint NOT NULL DEFAULT 0,
  retries bigint NOT NULL DEFAULT 0,
  rate_limit_responses bigint NOT NULL DEFAULT 0,
  last_api_latency_ms double precision NOT NULL DEFAULT 0,
  cycle_duration_ms double precision NOT NULL DEFAULT 0,
  message text
);
CREATE INDEX IF NOT EXISTS collector_cycles_completed_idx ON collector_cycles(cycle_completed_at DESC);

COMMIT;
