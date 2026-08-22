BEGIN;

CREATE TABLE IF NOT EXISTS item_signatures (
  exact_signature text PRIMARY KEY,
  base_signature text NOT NULL,
  modifiers text NOT NULL DEFAULT '',
  first_seen_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS item_signatures_base_idx ON item_signatures(base_signature);

CREATE TABLE IF NOT EXISTS auction_listings (
  fingerprint text PRIMARY KEY,
  authoritative_id text UNIQUE,
  item_signature text NOT NULL REFERENCES item_signatures(exact_signature),
  seller_uuid uuid,
  seller_name text NOT NULL,
  total_price bigint NOT NULL CHECK (total_price > 0),
  unit_price bigint NOT NULL CHECK (unit_price > 0),
  quantity integer NOT NULL CHECK (quantity BETWEEN 1 AND 1728),
  first_seen timestamptz NOT NULL,
  last_seen timestamptz NOT NULL,
  expires_at timestamptz,
  source text NOT NULL,
  search_context text,
  page integer,
  item_data jsonb NOT NULL,
  observer_count integer NOT NULL DEFAULT 1,
  active boolean NOT NULL DEFAULT true
);
CREATE INDEX IF NOT EXISTS auction_active_signature_price_idx ON auction_listings(item_signature, unit_price) WHERE active;
CREATE INDEX IF NOT EXISTS auction_active_last_seen_idx ON auction_listings(last_seen DESC) WHERE active;
CREATE INDEX IF NOT EXISTS auction_active_expiry_idx ON auction_listings(expires_at) WHERE active AND expires_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS listing_observations (
  id bigint GENERATED ALWAYS AS IDENTITY,
  fingerprint text NOT NULL REFERENCES auction_listings(fingerprint),
  client_id text NOT NULL,
  observed_at timestamptz NOT NULL,
  search_context text,
  page integer,
  latency_ns bigint,
  PRIMARY KEY (id, observed_at),
  UNIQUE (fingerprint, client_id, observed_at)
) PARTITION BY RANGE (observed_at);
CREATE INDEX IF NOT EXISTS observations_fingerprint_time_idx ON listing_observations(fingerprint, observed_at DESC);

CREATE TABLE IF NOT EXISTS transactions (
  id bigint GENERATED ALWAYS AS IDENTITY,
  fingerprint text NOT NULL,
  item_signature text NOT NULL REFERENCES item_signatures(exact_signature),
  seller_uuid uuid,
  seller_name text NOT NULL,
  total_price bigint NOT NULL CHECK (total_price > 0),
  unit_price bigint NOT NULL CHECK (unit_price > 0),
  quantity integer NOT NULL CHECK (quantity BETWEEN 1 AND 1728),
  sold_at timestamptz NOT NULL,
  source text NOT NULL,
  item_data jsonb NOT NULL,
  PRIMARY KEY (id, sold_at),
  UNIQUE (fingerprint, sold_at)
) PARTITION BY RANGE (sold_at);
CREATE INDEX IF NOT EXISTS transactions_signature_sold_idx ON transactions(item_signature, sold_at DESC) INCLUDE (unit_price, quantity);

CREATE TABLE IF NOT EXISTS transactions_default PARTITION OF transactions DEFAULT;
CREATE TABLE IF NOT EXISTS listing_observations_default PARTITION OF listing_observations DEFAULT;

CREATE TABLE IF NOT EXISTS valuations (
  item_signature text NOT NULL REFERENCES item_signatures(exact_signature),
  generated_at timestamptz NOT NULL,
  fair_value bigint NOT NULL,
  quick_sell_value bigint NOT NULL,
  active_best_ask bigint,
  confidence_bps integer NOT NULL CHECK (confidence_bps BETWEEN 0 AND 10000),
  sample_count integer NOT NULL,
  volume_24h integer NOT NULL,
  volatility_bps integer NOT NULL,
  model_version text NOT NULL,
  PRIMARY KEY (item_signature, generated_at)
);
CREATE INDEX IF NOT EXISTS valuations_generated_idx ON valuations(generated_at DESC);

CREATE TABLE IF NOT EXISTS clients (client_id text PRIMARY KEY, username text NOT NULL, role text NOT NULL, last_seen timestamptz NOT NULL, metadata jsonb NOT NULL DEFAULT '{}');
CREATE TABLE IF NOT EXISTS worker_sessions (session_id uuid PRIMARY KEY, client_id text NOT NULL REFERENCES clients(client_id), connected_at timestamptz NOT NULL, disconnected_at timestamptz, state jsonb NOT NULL);
CREATE TABLE IF NOT EXISTS assignments (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, client_id text NOT NULL REFERENCES clients(client_id), target text NOT NULL, score bigint NOT NULL, assigned_at timestamptz NOT NULL, ended_at timestamptz);
CREATE INDEX IF NOT EXISTS assignments_active_client_idx ON assignments(client_id) WHERE ended_at IS NULL;
CREATE TABLE IF NOT EXISTS flip_events (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, client_id text NOT NULL, fingerprint text NOT NULL, detected_at timestamptz NOT NULL, expected_profit bigint NOT NULL, decision_latency_ns bigint);
CREATE INDEX IF NOT EXISTS flip_events_detected_idx ON flip_events(detected_at DESC);
CREATE TABLE IF NOT EXISTS purchase_attempts (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, client_id text NOT NULL, fingerprint text NOT NULL, attempted_at timestamptz NOT NULL, completed_at timestamptz, success boolean, failure_reason text, mode text NOT NULL);
CREATE INDEX IF NOT EXISTS purchase_attempts_time_idx ON purchase_attempts(attempted_at DESC);
CREATE TABLE IF NOT EXISTS price_snapshots (version bigint PRIMARY KEY, generated_at timestamptz NOT NULL, entry_count integer NOT NULL, encoded_size integer NOT NULL, checksum text NOT NULL);

COMMIT;
