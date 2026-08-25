package orders

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

var memorySequence atomic.Uint64
var ErrDiagnosticRateLimit = errors.New("diagnostic rate limit exceeded")

type Store struct {
	db   *sql.DB
	now  func() time.Time
	path string
}

func OpenStore(path string) (*Store, error) {
	dsn := path
	if strings.TrimSpace(path) == "" {
		dsn = fmt.Sprintf("file:orders-memory-%d?mode=memory&cache=shared", memorySequence.Add(1))
	} else {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create order database directory: %w", err)
		}
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open order database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &Store{db: db, now: func() time.Time { return time.Now().UTC() }, path: strings.TrimSpace(path)}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) migrate() error {
	statements := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=NORMAL`,
		`PRAGMA foreign_keys=ON`,
		`PRAGMA busy_timeout=5000`,
		`CREATE TABLE IF NOT EXISTS observers (
			observer_id TEXT PRIMARY KEY, parser_version TEXT NOT NULL, proxy_label TEXT NOT NULL,
			state TEXT NOT NULL, current_task_id TEXT NOT NULL DEFAULT '', current_page INTEGER NOT NULL DEFAULT 0,
			latency_ms REAL NOT NULL DEFAULT 0, reconnect_count INTEGER NOT NULL DEFAULT 0,
			last_seen_ms INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY, kind TEXT NOT NULL, signature TEXT NOT NULL DEFAULT '', priority INTEGER NOT NULL,
			desired_freshness_ms INTEGER NOT NULL, parser_schema TEXT NOT NULL, state TEXT NOT NULL,
			assigned_observer TEXT NOT NULL DEFAULT '', lease_expires_ms INTEGER NOT NULL DEFAULT 0,
			lease_token TEXT NOT NULL DEFAULT '',
			created_ms INTEGER NOT NULL, updated_ms INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS tasks_ready ON tasks(state, priority DESC, created_ms)`,
		`CREATE TABLE IF NOT EXISTS scans (
			id INTEGER PRIMARY KEY AUTOINCREMENT, observer_id TEXT NOT NULL, task_id TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL, content_hash TEXT NOT NULL, screen_title TEXT NOT NULL,
			page INTEGER NOT NULL, complete INTEGER NOT NULL, unknown_schema INTEGER NOT NULL DEFAULT 0,
			schema_reason TEXT NOT NULL DEFAULT '', observed_ms INTEGER NOT NULL, received_ms INTEGER NOT NULL,
			UNIQUE(observer_id, session_id, content_hash))`,
		`CREATE INDEX IF NOT EXISTS scans_observed ON scans(observed_ms DESC)`,
		`CREATE TABLE IF NOT EXISTS order_rows (
			id INTEGER PRIMARY KEY AUTOINCREMENT, scan_id INTEGER NOT NULL REFERENCES scans(id) ON DELETE CASCADE,
			observer_id TEXT NOT NULL, order_key TEXT NOT NULL, item_id TEXT NOT NULL, signature TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '', quantity INTEGER NOT NULL, max_stack_size INTEGER NOT NULL,
			unit_reward INTEGER NOT NULL DEFAULT 0, unit_reward_cents INTEGER NOT NULL,
			requested_quantity INTEGER NOT NULL, remaining_quantity INTEGER NOT NULL,
			owner TEXT NOT NULL DEFAULT '', expires_ms INTEGER NOT NULL DEFAULT 0, price_position INTEGER NOT NULL DEFAULT 0,
			slot INTEGER NOT NULL, raw_field_hash TEXT NOT NULL, signature_complete INTEGER NOT NULL DEFAULT 0, observed_ms INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS order_rows_signature_time ON order_rows(signature, observed_ms DESC)`,
		`CREATE INDEX IF NOT EXISTS order_rows_order_time ON order_rows(observer_id, order_key, observed_ms DESC)`,
		`CREATE TABLE IF NOT EXISTS order_evidence_summary (
			signature TEXT PRIMARY KEY,item_id TEXT NOT NULL,display_name TEXT NOT NULL DEFAULT '',
			complete_scans INTEGER NOT NULL,first_seen_ms INTEGER NOT NULL,last_seen_ms INTEGER NOT NULL,
			observed_quantity INTEGER NOT NULL,max_stack_size INTEGER NOT NULL,available_units INTEGER NOT NULL DEFAULT 0)`,
		`CREATE INDEX IF NOT EXISTS order_evidence_recent ON order_evidence_summary(last_seen_ms DESC)`,
		`CREATE TABLE IF NOT EXISTS fill_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT, signature TEXT NOT NULL, order_key TEXT NOT NULL,
			observer_id TEXT NOT NULL, units INTEGER NOT NULL, unit_reward INTEGER NOT NULL DEFAULT 0,
			unit_reward_cents INTEGER NOT NULL, observed_ms INTEGER NOT NULL,
			UNIQUE(observer_id, order_key, observed_ms))`,
		`CREATE INDEX IF NOT EXISTS fill_signature_time ON fill_events(signature, observed_ms DESC)`,
		`CREATE TABLE IF NOT EXISTS watches (
			id TEXT PRIMARY KEY, signature TEXT NOT NULL, created_ms INTEGER NOT NULL, expires_ms INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS watches_expiry ON watches(expires_ms)`,
		`CREATE TABLE IF NOT EXISTS diagnostics (
			id INTEGER PRIMARY KEY AUTOINCREMENT, install_id TEXT NOT NULL, version TEXT NOT NULL,
			event TEXT NOT NULL, code TEXT NOT NULL DEFAULT '', duration_ms INTEGER NOT NULL DEFAULT 0,
			fields_json TEXT NOT NULL DEFAULT '{}', created_ms INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS diagnostics_created ON diagnostics(created_ms DESC)`,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("migrate order database: %w", err)
		}
	}
	// Existing development databases predate authenticated leases.
	if _, err := s.db.Exec(`ALTER TABLE tasks ADD COLUMN lease_token TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return fmt.Errorf("migrate task lease token: %w", err)
	}
	if _, err := s.db.Exec(`ALTER TABLE order_rows ADD COLUMN signature_complete INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return fmt.Errorf("migrate signature completeness: %w", err)
	}
	// Never reinterpret the legacy whole-dollar columns as cents. Existing rows
	// receive zero in the new columns and are therefore excluded from economics;
	// fresh observations repopulate exact cent values without an unsafe guess.
	if _, err := s.db.Exec(`ALTER TABLE order_rows ADD COLUMN unit_reward_cents INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return fmt.Errorf("migrate exact order reward: %w", err)
	}
	if _, err := s.db.Exec(`ALTER TABLE fill_events ADD COLUMN unit_reward_cents INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return fmt.Errorf("migrate exact fill reward: %w", err)
	}
	// Legacy reductions were inferred across arbitrarily old scans and are not
	// strong enough to drive purchases. They remain available for audit at level
	// zero; only short-gap, repeated observations written below are confirmed.
	if _, err := s.db.Exec(`ALTER TABLE fill_events ADD COLUMN confirmation_level INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return fmt.Errorf("migrate fill confirmation: %w", err)
	}
	if _, err := s.db.Exec(`ALTER TABLE fill_events ADD COLUMN previous_observed_ms INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return fmt.Errorf("migrate fill observation interval: %w", err)
	}
	availabilityAdded := false
	if _, err := s.db.Exec(`ALTER TABLE order_evidence_summary ADD COLUMN available_units INTEGER NOT NULL DEFAULT 0`); err == nil {
		availabilityAdded = true
	} else if !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return fmt.Errorf("migrate order availability summary: %w", err)
	}
	// Backfill once for databases created before the incremental summary. Future
	// scans update this table transactionally and avoid regrouping the full row
	// history on every candidate/dashboard refresh.
	var summaryCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM order_evidence_summary`).Scan(&summaryCount); err != nil {
		return fmt.Errorf("count order evidence summary: %w", err)
	}
	if summaryCount == 0 {
		if _, err := s.db.Exec(`WITH base AS (
			SELECT signature,MAX(item_id) AS item_id,MAX(display_name) AS display_name,COUNT(DISTINCT scan_id) AS complete_scans,
			MIN(observed_ms) AS first_seen_ms,MAX(observed_ms) AS last_seen_ms,MAX(quantity) AS observed_quantity,MAX(max_stack_size) AS max_stack_size
			FROM order_rows WHERE unit_reward_cents>0 GROUP BY signature), latest AS (
			SELECT signature,MAX(observed_ms) AS observed_ms FROM order_rows WHERE unit_reward_cents>0 GROUP BY signature), available AS (
			SELECT r.signature,SUM(r.remaining_quantity) AS units FROM order_rows r JOIN latest l ON l.signature=r.signature AND l.observed_ms=r.observed_ms
			WHERE r.unit_reward_cents>0 GROUP BY r.signature)
			INSERT INTO order_evidence_summary(signature,item_id,display_name,complete_scans,first_seen_ms,last_seen_ms,observed_quantity,max_stack_size,available_units)
			SELECT b.signature,b.item_id,b.display_name,b.complete_scans,b.first_seen_ms,b.last_seen_ms,b.observed_quantity,b.max_stack_size,COALESCE(a.units,0)
			FROM base b LEFT JOIN available a ON a.signature=b.signature`); err != nil {
			return fmt.Errorf("backfill order evidence summary: %w", err)
		}
	} else if availabilityAdded {
		if _, err := s.db.Exec(`UPDATE order_evidence_summary SET available_units=COALESCE((SELECT SUM(r.remaining_quantity)
			FROM order_rows r WHERE r.signature=order_evidence_summary.signature AND r.unit_reward_cents>0 AND r.observed_ms=(
			SELECT MAX(latest.observed_ms) FROM order_rows latest WHERE latest.signature=order_evidence_summary.signature AND latest.unit_reward_cents>0)),0)`); err != nil {
			return fmt.Errorf("backfill order availability: %w", err)
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Register(ctx context.Context, registration ObserverRegistration) (Observer, error) {
	now := s.now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO observers(observer_id,parser_version,proxy_label,state,last_seen_ms)
		VALUES(?,?,?,'registered',?) ON CONFLICT(observer_id) DO UPDATE SET
		parser_version=excluded.parser_version,proxy_label=excluded.proxy_label,state='registered',last_seen_ms=excluded.last_seen_ms`,
		registration.ObserverID, registration.ParserVersion, registration.ProxyLabel, now.UnixMilli())
	if err != nil {
		return Observer{}, err
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE kind='discovery' AND assigned_observer=? AND state IN ('ready','leased')`, registration.ObserverID).Scan(&count); err != nil {
		return Observer{}, err
	}
	if count == 0 {
		id := newID("task")
		_, err = s.db.ExecContext(ctx, `INSERT INTO tasks(id,kind,priority,desired_freshness_ms,parser_schema,state,assigned_observer,created_ms,updated_ms)
			VALUES(?,'discovery',10,5000,?,'ready',?,?,?)`, id, SchemaVersion, registration.ObserverID, now.UnixMilli(), now.Add(-5*time.Second).UnixMilli())
		if err != nil {
			return Observer{}, err
		}
	}
	return s.observer(ctx, registration.ObserverID)
}

func (s *Store) Heartbeat(ctx context.Context, heartbeat Heartbeat) error {
	now := s.now()
	result, err := s.db.ExecContext(ctx, `UPDATE observers SET state=?,current_task_id=?,current_page=?,latency_ms=?,reconnect_count=?,last_seen_ms=? WHERE observer_id=?`,
		heartbeat.State, heartbeat.TaskID, heartbeat.Page, heartbeat.LatencyMS, heartbeat.ReconnectCount, now.UnixMilli(), heartbeat.ObserverID)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return errors.New("observer is not registered")
	}
	if heartbeat.TaskID != "" {
		result, err := s.db.ExecContext(ctx, `UPDATE tasks SET lease_expires_ms=?,updated_ms=? WHERE id=? AND state='leased' AND assigned_observer=? AND lease_token=? AND lease_expires_ms>?`,
			now.Add(30*time.Second).UnixMilli(), now.UnixMilli(), heartbeat.TaskID, heartbeat.ObserverID, heartbeat.LeaseToken, now.UnixMilli())
		if err != nil {
			return err
		}
		updated, _ := result.RowsAffected()
		if updated != 1 {
			var state, assignedObserver string
			lookupErr := s.db.QueryRowContext(ctx, `SELECT state,assigned_observer FROM tasks WHERE id=?`, heartbeat.TaskID).Scan(&state, &assignedObserver)
			if lookupErr == nil && assignedObserver == heartbeat.ObserverID && state != "leased" {
				_, clearErr := s.db.ExecContext(ctx, `UPDATE observers SET current_task_id='',current_page=0 WHERE observer_id=? AND current_task_id=?`, heartbeat.ObserverID, heartbeat.TaskID)
				return clearErr
			}
			return errors.New("task lease is invalid or expired")
		}
	}
	return nil
}

func (s *Store) LeaseTask(ctx context.Context, observerID string, lease time.Duration) (*Task, error) {
	now := s.now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `UPDATE tasks SET
		state=CASE WHEN kind='focused_watch' AND NOT EXISTS(SELECT 1 FROM watches WHERE watches.signature=tasks.signature AND watches.expires_ms>?) THEN 'completed' ELSE 'ready' END,
		assigned_observer=CASE WHEN kind='discovery' THEN assigned_observer ELSE '' END,lease_expires_ms=0,lease_token='',updated_ms=?
		WHERE state='leased' AND lease_expires_ms<?`, now.UnixMilli(), now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return nil, err
	}
	row := tx.QueryRowContext(ctx, `SELECT id,kind,signature,priority,desired_freshness_ms,parser_schema,lease_expires_ms,lease_token FROM tasks
		WHERE state='leased' AND assigned_observer=? AND lease_expires_ms>? ORDER BY priority DESC LIMIT 1`, observerID, now.UnixMilli())
	task, scanErr := scanTask(row)
	if scanErr != nil && !errors.Is(scanErr, sql.ErrNoRows) {
		return nil, scanErr
	}
	if task != nil {
		task.LeaseExpiresAt = now.Add(lease)
		result, updateErr := tx.ExecContext(ctx, `UPDATE tasks SET lease_expires_ms=?,updated_ms=? WHERE id=? AND state='leased' AND assigned_observer=? AND lease_token=?`,
			task.LeaseExpiresAt.UnixMilli(), now.UnixMilli(), task.ID, observerID, task.LeaseToken)
		if updateErr != nil {
			return nil, updateErr
		}
		rows, _ := result.RowsAffected()
		if rows != 1 {
			return nil, errors.New("task lease resume raced")
		}
	}
	if task == nil {
		row = tx.QueryRowContext(ctx, `SELECT id,kind,signature,priority,desired_freshness_ms,parser_schema,lease_expires_ms,lease_token FROM tasks
			WHERE state='ready' AND (assigned_observer='' OR assigned_observer=?) AND updated_ms+desired_freshness_ms<=? ORDER BY priority DESC,created_ms LIMIT 1`, observerID, now.UnixMilli())
		task, scanErr = scanTask(row)
		if errors.Is(scanErr, sql.ErrNoRows) {
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			return nil, nil
		}
		if scanErr != nil {
			return nil, scanErr
		}
		task.LeaseExpiresAt = now.Add(lease)
		task.LeaseToken = newID("lease")
		result, updateErr := tx.ExecContext(ctx, `UPDATE tasks SET state='leased',assigned_observer=?,lease_expires_ms=?,lease_token=?,updated_ms=? WHERE id=? AND state='ready'`, observerID, task.LeaseExpiresAt.UnixMilli(), task.LeaseToken, now.UnixMilli(), task.ID)
		if updateErr != nil {
			return nil, updateErr
		}
		rows, _ := result.RowsAffected()
		if rows != 1 {
			return nil, errors.New("task lease raced")
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return task, nil
}

type rowScanner interface{ Scan(...any) error }

func scanTask(row rowScanner) (*Task, error) {
	var task Task
	var expires int64
	if err := row.Scan(&task.ID, &task.Kind, &task.Signature, &task.Priority, &task.DesiredFreshness, &task.ParserSchema, &expires, &task.LeaseToken); err != nil {
		return nil, err
	}
	task.LeaseExpiresAt = time.UnixMilli(expires).UTC()
	return &task, nil
}

func (s *Store) SaveScan(ctx context.Context, batch ScanBatch) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var registered int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM observers WHERE observer_id=?`, batch.ObserverID).Scan(&registered); err != nil {
		return false, err
	}
	if registered != 1 {
		return false, errors.New("observer is not registered")
	}
	if batch.TaskID != "" {
		var leased int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE id=? AND state='leased' AND assigned_observer=? AND lease_token=? AND lease_expires_ms>?`,
			batch.TaskID, batch.ObserverID, batch.LeaseToken, s.now().UnixMilli()).Scan(&leased); err != nil {
			return false, err
		}
		if leased != 1 {
			return false, errors.New("task lease is invalid or expired")
		}
	}
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO scans(observer_id,task_id,session_id,content_hash,screen_title,page,complete,unknown_schema,schema_reason,observed_ms,received_ms)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`, batch.ObserverID, batch.TaskID, batch.SessionID, batch.ContentHash, batch.ScreenTitle,
		batch.Page, boolInt(batch.Complete), boolInt(batch.UnknownSchema), batch.SchemaReason, batch.ObservedAt.UnixMilli(), s.now().UnixMilli())
	if err != nil {
		return false, err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return false, tx.Commit()
	}
	scanID, err := result.LastInsertId()
	if err != nil {
		return false, err
	}
	// Capture-only, partial, and unknown layouts are retained as scan diagnostics,
	// but never enter economic evidence or fill inference.
	if batch.Complete && !batch.UnknownSchema {
		type summaryObservation struct {
			order          OrderObservation
			availableUnits int64
		}
		summaries := make(map[string]summaryObservation, len(batch.Orders))
		for _, order := range batch.Orders {
			summary := summaries[order.Signature]
			summary.order = order
			summary.availableUnits += order.RemainingQuantity
			summaries[order.Signature] = summary
		}
		for _, order := range batch.Orders {
			var previous, previousObserved int64
			previousErr := tx.QueryRowContext(ctx, `SELECT remaining_quantity,observed_ms FROM order_rows WHERE observer_id=? AND order_key=? AND unit_reward_cents=? AND observed_ms<? ORDER BY observed_ms DESC LIMIT 1`,
				batch.ObserverID, order.OrderKey, order.UnitRewardCents, batch.ObservedAt.UnixMilli()).Scan(&previous, &previousObserved)
			_, err = tx.ExecContext(ctx, `INSERT INTO order_rows(scan_id,observer_id,order_key,item_id,signature,display_name,quantity,max_stack_size,unit_reward,unit_reward_cents,requested_quantity,remaining_quantity,owner,expires_ms,price_position,slot,raw_field_hash,signature_complete,observed_ms)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, scanID, batch.ObserverID, order.OrderKey, order.ItemID, order.Signature,
				order.DisplayName, order.Quantity, order.MaxStackSize, 0, order.UnitRewardCents, order.RequestedQuantity, order.RemainingQuantity,
				order.Owner, timeMillis(order.ExpiresAt), order.PricePosition, order.Slot, order.RawFieldHash, boolInt(order.SignatureComplete), batch.ObservedAt.UnixMilli())
			if err != nil {
				return false, err
			}
			if previousErr == nil && previous > order.RemainingQuantity && batch.Complete {
				confirmation := 0
				gap := batch.ObservedAt.UnixMilli() - previousObserved
				if gap > 0 && gap <= (2*time.Minute).Milliseconds() {
					confirmation = 2
				}
				_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO fill_events(signature,order_key,observer_id,units,unit_reward,unit_reward_cents,confirmation_level,previous_observed_ms,observed_ms) VALUES(?,?,?,?,?,?,?,?,?)`,
					order.Signature, order.OrderKey, batch.ObserverID, previous-order.RemainingQuantity, 0, order.UnitRewardCents, confirmation, previousObserved, batch.ObservedAt.UnixMilli())
				if err != nil {
					return false, err
				}
			} else if previousErr != nil && !errors.Is(previousErr, sql.ErrNoRows) {
				return false, previousErr
			}
		}
		for _, summary := range summaries {
			order := summary.order
			_, err = tx.ExecContext(ctx, `INSERT INTO order_evidence_summary(signature,item_id,display_name,complete_scans,first_seen_ms,last_seen_ms,observed_quantity,max_stack_size,available_units)
				VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(signature) DO UPDATE SET
				item_id=excluded.item_id,display_name=excluded.display_name,complete_scans=order_evidence_summary.complete_scans+1,
				first_seen_ms=MIN(order_evidence_summary.first_seen_ms,excluded.first_seen_ms),last_seen_ms=MAX(order_evidence_summary.last_seen_ms,excluded.last_seen_ms),
				observed_quantity=excluded.observed_quantity,max_stack_size=MAX(order_evidence_summary.max_stack_size,excluded.max_stack_size),available_units=excluded.available_units`,
				order.Signature, order.ItemID, order.DisplayName, 1, batch.ObservedAt.UnixMilli(), batch.ObservedAt.UnixMilli(), order.Quantity, order.MaxStackSize, summary.availableUnits)
			if err != nil {
				return false, err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE observers SET state=?,current_page=?,current_task_id=?,last_seen_ms=? WHERE observer_id=?`,
		map[bool]string{true: "schema_hold", false: "scanning"}[batch.UnknownSchema], batch.Page, batch.TaskID, s.now().UnixMilli(), batch.ObserverID)
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *Store) CompleteTask(ctx context.Context, result TaskResult) error {
	now := s.now()
	var kind, signature string
	if err := s.db.QueryRowContext(ctx, `SELECT kind,signature FROM tasks WHERE id=? AND state='leased' AND assigned_observer=? AND lease_token=? AND lease_expires_ms>?`, result.TaskID, result.ObserverID, result.LeaseToken, now.UnixMilli()).Scan(&kind, &signature); err != nil {
		return err
	}
	state := "completed"
	assigned := result.ObserverID
	if result.Status == "retry" || (kind == "discovery" && result.Status == "complete") {
		state = "ready"
	}
	if kind == "focused_watch" {
		var active int
		_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM watches WHERE signature=? AND expires_ms>?`, signature, now.UnixMilli()).Scan(&active)
		if active > 0 {
			state = "ready"
			assigned = ""
		}
	}
	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET state=?,assigned_observer=?,lease_expires_ms=0,lease_token='',updated_ms=? WHERE id=?`, state, assigned, now.UnixMilli(), result.TaskID)
	return err
}

func (s *Store) AddWatch(ctx context.Context, signature string, lifetime time.Duration) (Watch, error) {
	now := s.now()
	watch := Watch{ID: newID("watch"), Signature: signature, CreatedAt: now, ExpiresAt: now.Add(lifetime)}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Watch{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO watches(id,signature,created_ms,expires_ms) VALUES(?,?,?,?)`, watch.ID, watch.Signature, watch.CreatedAt.UnixMilli(), watch.ExpiresAt.UnixMilli()); err != nil {
		return Watch{}, err
	}
	var activeTask int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE kind='focused_watch' AND signature=? AND state IN ('ready','leased')`, signature).Scan(&activeTask); err != nil {
		return Watch{}, err
	}
	if activeTask == 0 {
		if _, err = tx.ExecContext(ctx, `INSERT INTO tasks(id,kind,signature,priority,desired_freshness_ms,parser_schema,state,created_ms,updated_ms)
			VALUES(?,'focused_watch',?,100,1000,?,'ready',?,?)`, newID("task"), signature, SchemaVersion, now.UnixMilli(), now.Add(-time.Second).UnixMilli()); err != nil {
			return Watch{}, err
		}
	}
	return watch, tx.Commit()
}

func (s *Store) DeleteWatch(ctx context.Context, id string) error {
	var signature string
	if err := s.db.QueryRowContext(ctx, `SELECT signature FROM watches WHERE id=?`, id).Scan(&signature); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM watches WHERE id=?`, id); err != nil {
		return err
	}
	var remaining int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM watches WHERE signature=? AND expires_ms>?`, signature, s.now().UnixMilli()).Scan(&remaining); err != nil {
		return err
	}
	if remaining == 0 {
		if _, err = tx.ExecContext(ctx, `UPDATE tasks SET state='completed',lease_expires_ms=0,lease_token='' WHERE kind='focused_watch' AND signature=? AND state='ready'`, signature); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SaveDiagnostic(ctx context.Context, diagnostic Diagnostic) error {
	encoded, err := json.Marshal(diagnostic.Fields)
	if err != nil {
		return err
	}
	created := diagnostic.CreatedAt
	if created.IsZero() {
		created = s.now()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var recent int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM diagnostics WHERE install_id=? AND created_ms>=?`, diagnostic.InstallID, s.now().Add(-time.Hour).UnixMilli()).Scan(&recent); err != nil {
		return err
	}
	if recent >= 500 {
		return ErrDiagnosticRateLimit
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO diagnostics(install_id,version,event,code,duration_ms,fields_json,created_ms) VALUES(?,?,?,?,?,?,?)`,
		diagnostic.InstallID, diagnostic.Version, diagnostic.Event, diagnostic.Code, diagnostic.Duration, string(encoded), created.UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Evidence(ctx context.Context) ([]Evidence, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT signature,item_id,display_name,complete_scans,first_seen_ms,last_seen_ms,observed_quantity,max_stack_size,available_units
		FROM order_evidence_summary ORDER BY last_seen_ms DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	result := []Evidence{}
	for rows.Next() {
		var evidence Evidence
		var first, last int64
		if err := rows.Scan(&evidence.Signature, &evidence.ItemID, &evidence.DisplayName, &evidence.CompleteScans,
			&first, &last, &evidence.ObservedQuantity, &evidence.MaxStackSize, &evidence.AvailableUnits); err != nil {
			return nil, err
		}
		evidence.FirstSeenAt = time.UnixMilli(first).UTC()
		evidence.LastSeenAt = time.UnixMilli(last).UTC()
		result = append(result, evidence)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := s.enrichEvidence(ctx, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) enrichEvidence(ctx context.Context, evidence []Evidence) error {
	indexes := make(map[string]int, len(evidence))
	prices := make(map[string][]int64, len(evidence))
	observerPrices := make(map[string]map[string]int64, len(evidence))
	for index := range evidence {
		indexes[evidence[index].Signature] = index
		// The historical aggregate is useful for identity only. Current market
		// fields are rebuilt below from the short freshness window.
		evidence[index].BestUnitRewardCents = 0
		evidence[index].BestPricePosition = 0
		evidence[index].SignatureComplete = false
	}

	fillRows, err := s.db.QueryContext(ctx, `SELECT signature,COUNT(*),COALESCE(SUM(units),0),COUNT(DISTINCT order_key)
		FROM fill_events WHERE unit_reward_cents>0 AND confirmation_level>=2 AND observed_ms>=? GROUP BY signature`, s.now().Add(-24*time.Hour).UnixMilli())
	if err != nil {
		return err
	}
	for fillRows.Next() {
		var signature string
		var events int
		var units int64
		var orders int
		if err := fillRows.Scan(&signature, &events, &units, &orders); err != nil {
			fillRows.Close()
			return err
		}
		if index, ok := indexes[signature]; ok {
			evidence[index].FillEvents, evidence[index].FilledUnits24h, evidence[index].DistinctOrders = events, units, orders
		}
	}
	if err := closeRows(fillRows); err != nil {
		return err
	}

	recent := s.now().Add(-2 * time.Minute).UnixMilli()
	completeRows, err := s.db.QueryContext(ctx, `SELECT signature,MIN(signature_complete) FROM order_rows
		WHERE unit_reward_cents>0 AND observed_ms>=? GROUP BY signature`, recent)
	if err != nil {
		return err
	}
	for completeRows.Next() {
		var signature string
		var complete bool
		if err := completeRows.Scan(&signature, &complete); err != nil {
			completeRows.Close()
			return err
		}
		if index, ok := indexes[signature]; ok {
			evidence[index].SignatureComplete = complete
		}
	}
	if err := closeRows(completeRows); err != nil {
		return err
	}

	priceRows, err := s.db.QueryContext(ctx, `WITH samples AS (
		SELECT signature,MAX(unit_reward_cents) AS reward,
		COALESCE(MIN(CASE WHEN price_position>0 THEN price_position END),0) AS position,observer_id,
		ROW_NUMBER() OVER (PARTITION BY signature ORDER BY MAX(observed_ms) DESC) AS sample_rank
		FROM order_rows WHERE unit_reward_cents>0 AND observed_ms>=? GROUP BY signature,scan_id,observer_id)
		SELECT signature,reward,position,observer_id FROM samples WHERE sample_rank<=32`, recent)
	if err != nil {
		return err
	}
	for priceRows.Next() {
		var signature, observer string
		var price int64
		var position int
		if err := priceRows.Scan(&signature, &price, &position, &observer); err != nil {
			priceRows.Close()
			return err
		}
		index, ok := indexes[signature]
		if !ok {
			continue
		}
		prices[signature] = append(prices[signature], price)
		evidence[index].BestUnitRewardCents = max64(evidence[index].BestUnitRewardCents, price)
		if _, exists := observerPrices[signature]; !exists {
			observerPrices[signature] = map[string]int64{}
		}
		if _, exists := observerPrices[signature][observer]; !exists {
			observerPrices[signature][observer] = price
		}
	}
	if err := closeRows(priceRows); err != nil {
		return err
	}

	for index := range evidence {
		currentPrices := prices[evidence[index].Signature]
		evidence[index].Stable = stablePrices(currentPrices)
		if evidence[index].BestUnitRewardCents > 0 {
			// A new order one tick above the best targets queue position one; this
			// is not presented as a parsed server queue value.
			evidence[index].BestPricePosition = 1
		}
		for _, left := range observerPrices[evidence[index].Signature] {
			for _, right := range observerPrices[evidence[index].Signature] {
				if left > 0 && right > 0 && math.Abs(float64(left-right))/float64(max64(left, right)) > 0.10 {
					evidence[index].Conflict = true
				}
			}
		}
		classifyEvidence(&evidence[index], len(currentPrices))
	}
	return nil
}

func closeRows(rows *sql.Rows) error {
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	return rows.Close()
}

func classifyEvidence(evidence *Evidence, priceSamples int) {
	span := evidence.LastSeenAt.Sub(evidence.FirstSeenAt)
	evidence.Tier = "captured"
	evidence.Reason = "waiting for repeated complete scans"
	if evidence.CompleteScans >= 3 && span >= 10*time.Minute {
		evidence.Tier = "research"
		evidence.Reason = "waiting for confirmed fills and independent orders"
	}
	if evidence.CompleteScans >= 3 && span >= 15*time.Minute && evidence.FillEvents >= 5 && evidence.DistinctOrders >= 3 && evidence.Stable && !evidence.Conflict {
		evidence.Tier = "actionable"
		evidence.Reason = ""
	}
	if !evidence.SignatureComplete {
		evidence.Tier = "research"
		evidence.Reason = "canonical modifier signature has not been verified"
	}
	if evidence.Conflict {
		evidence.Tier = "hold"
		evidence.Reason = "observers disagree on current price"
	}
	if !evidence.Stable && priceSamples >= 3 {
		evidence.Tier = "hold"
		evidence.Reason = "order price is moving rapidly"
	}
}

func stablePrices(values []int64) bool {
	if len(values) < 3 {
		return false
	}
	copyValues := append([]int64(nil), values...)
	sort.Slice(copyValues, func(i, j int) bool { return copyValues[i] < copyValues[j] })
	median := copyValues[len(copyValues)/2]
	if median <= 0 {
		return false
	}
	return copyValues[len(copyValues)-1]-copyValues[0] <= median/10
}

func (s *Store) Debug(ctx context.Context) (DebugSnapshot, error) {
	evidence, err := s.Evidence(ctx)
	if err != nil {
		return DebugSnapshot{}, err
	}
	observers, err := s.Observers(ctx)
	if err != nil {
		return DebugSnapshot{}, err
	}
	watches, err := s.Watches(ctx)
	if err != nil {
		return DebugSnapshot{}, err
	}
	var diagnostics int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM diagnostics WHERE created_ms>=?`, s.now().Add(-14*24*time.Hour).UnixMilli()).Scan(&diagnostics); err != nil {
		return DebugSnapshot{}, err
	}
	coverage, err := s.scanCoverage(ctx)
	if err != nil {
		return DebugSnapshot{}, err
	}
	fills, err := s.recentFills(ctx)
	if err != nil {
		return DebugSnapshot{}, err
	}
	return DebugSnapshot{Observers: observers, Evidence: evidence, Watches: watches, ScanCoverage: coverage, RecentFills: fills, Diagnostics: diagnostics, GeneratedAt: s.now()}, nil
}

func (s *Store) scanCoverage(ctx context.Context) (ScanCoverage, error) {
	var value ScanCoverage
	var last int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(complete),0),COALESCE(SUM(CASE WHEN complete=0 THEN 1 ELSE 0 END),0),COALESCE(SUM(unknown_schema),0),COUNT(DISTINCT page),COALESCE(MAX(page),0),COALESCE(MAX(observed_ms),0) FROM scans`).
		Scan(&value.Total, &value.Complete, &value.Incomplete, &value.UnknownSchema, &value.DistinctPages, &value.HighestPage, &last)
	if err != nil {
		return value, err
	}
	if err = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN confirmation_level>=2 THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN confirmation_level<2 THEN 1 ELSE 0 END),0) FROM fill_events WHERE unit_reward_cents>0`).Scan(&value.ConfirmedFills, &value.QuarantinedFills); err != nil {
		return value, err
	}
	if err = s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT signature) FROM order_rows WHERE signature_complete=1 AND unit_reward_cents>0 AND observed_ms>=?`, s.now().Add(-2*time.Minute).UnixMilli()).Scan(&value.CompleteSignatures); err != nil {
		return value, err
	}
	if last > 0 {
		value.LastScanAt = time.UnixMilli(last).UTC()
	}
	return value, err
}

func (s *Store) recentFills(ctx context.Context) ([]FillEvidence, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT signature,order_key,observer_id,units,unit_reward_cents,confirmation_level,previous_observed_ms,observed_ms FROM fill_events WHERE unit_reward_cents>0 AND confirmation_level>=2 ORDER BY observed_ms DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []FillEvidence{}
	for rows.Next() {
		var value FillEvidence
		var previous, observed int64
		if err := rows.Scan(&value.Signature, &value.OrderKey, &value.ObserverID, &value.Units, &value.UnitRewardCents, &value.ConfirmationLevel, &previous, &observed); err != nil {
			return nil, err
		}
		value.PreviousObservedAt = time.UnixMilli(previous).UTC()
		value.ObservedAt = time.UnixMilli(observed).UTC()
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) Observers(ctx context.Context) ([]Observer, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT observer_id,parser_version,proxy_label,state,current_task_id,current_page,latency_ms,reconnect_count,last_seen_ms FROM observers ORDER BY observer_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Observer{}
	for rows.Next() {
		var observer Observer
		var last int64
		if err := rows.Scan(&observer.ObserverID, &observer.ParserVersion, &observer.ProxyLabel, &observer.State, &observer.CurrentTaskID, &observer.CurrentPage, &observer.LatencyMS, &observer.ReconnectCount, &last); err != nil {
			return nil, err
		}
		observer.LastSeenAt = time.UnixMilli(last).UTC()
		s.markObserverOffline(&observer)
		result = append(result, observer)
	}
	return result, rows.Err()
}

func (s *Store) Watches(ctx context.Context) ([]Watch, error) {
	now := s.now()
	_, _ = s.db.ExecContext(ctx, `DELETE FROM watches WHERE expires_ms<=?`, now.UnixMilli())
	rows, err := s.db.QueryContext(ctx, `SELECT id,signature,created_ms,expires_ms FROM watches ORDER BY created_ms DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Watch{}
	for rows.Next() {
		var watch Watch
		var created, expires int64
		if err := rows.Scan(&watch.ID, &watch.Signature, &created, &expires); err != nil {
			return nil, err
		}
		watch.CreatedAt, watch.ExpiresAt = time.UnixMilli(created).UTC(), time.UnixMilli(expires).UTC()
		result = append(result, watch)
	}
	return result, rows.Err()
}

func (s *Store) Cleanup(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM scans WHERE observed_ms<?`, s.now().Add(-7*24*time.Hour).UnixMilli()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM fill_events WHERE observed_ms<?`, s.now().Add(-90*24*time.Hour).UnixMilli()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM diagnostics WHERE created_ms<?`, s.now().Add(-14*24*time.Hour).UnixMilli()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM watches WHERE expires_ms<?`, s.now().UnixMilli()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE tasks SET state='completed',lease_expires_ms=0,lease_token='' WHERE kind='focused_watch' AND state='ready' AND signature NOT IN (SELECT signature FROM watches WHERE expires_ms>?)`, s.now().UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Backup(ctx context.Context) (string, error) {
	if s.path == "" {
		return "", nil
	}
	directory := s.path + ".backups"
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	target := filepath.Join(directory, s.now().Format("20060102T150405Z")+".db")
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, target); err != nil {
		return "", fmt.Errorf("backup order database: %w", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", fmt.Errorf("list order backups: %w", err)
	}
	cutoff := s.now().Add(-7 * 24 * time.Hour)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".db" {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return "", fmt.Errorf("inspect order backup: %w", infoErr)
		}
		if info.ModTime().Before(cutoff) {
			if removeErr := os.Remove(filepath.Join(directory, entry.Name())); removeErr != nil {
				return "", fmt.Errorf("prune order backup: %w", removeErr)
			}
		}
	}
	return target, nil
}

func (s *Store) observer(ctx context.Context, id string) (Observer, error) {
	var observer Observer
	var last int64
	err := s.db.QueryRowContext(ctx, `SELECT observer_id,parser_version,proxy_label,state,current_task_id,current_page,latency_ms,reconnect_count,last_seen_ms FROM observers WHERE observer_id=?`, id).
		Scan(&observer.ObserverID, &observer.ParserVersion, &observer.ProxyLabel, &observer.State, &observer.CurrentTaskID, &observer.CurrentPage, &observer.LatencyMS, &observer.ReconnectCount, &last)
	observer.LastSeenAt = time.UnixMilli(last).UTC()
	s.markObserverOffline(&observer)
	return observer, err
}

func (s *Store) markObserverOffline(observer *Observer) {
	if !observer.LastSeenAt.IsZero() && s.now().Sub(observer.LastSeenAt) > 15*time.Second {
		observer.State = "offline"
	}
}

func newID(prefix string) string {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(raw)
}

func timeMillis(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixMilli()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
func max64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
