package state

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"donut-network/internal/market"
)

const formatVersion = 1

type File struct {
	path      string
	retention time.Duration
	limit     int
}

type document struct {
	Version      int                  `json:"version"`
	SavedAt      time.Time            `json:"saved_at"`
	Transactions []market.Transaction `json:"transactions"`
}

func NewFile(path string, retention time.Duration, limit int) *File {
	if retention <= 0 {
		retention = 31 * 24 * time.Hour
	}
	if limit <= 0 {
		limit = 100_000
	}
	return &File{path: path, retention: retention, limit: limit}
}

func (f *File) Load() ([]market.Transaction, error) {
	if f.path == "" {
		return nil, nil
	}
	transactions, primaryErr := read(f.path)
	if primaryErr == nil {
		return Merge(nil, transactions, time.Now().UTC(), f.retention, f.limit), nil
	}
	transactions, backupErr := read(f.path + ".bak")
	if backupErr == nil {
		return Merge(nil, transactions, time.Now().UTC(), f.retention, f.limit), nil
	}
	if errors.Is(primaryErr, os.ErrNotExist) && errors.Is(backupErr, os.ErrNotExist) {
		return nil, nil
	}
	if !errors.Is(primaryErr, os.ErrNotExist) {
		return nil, primaryErr
	}
	return nil, backupErr
}

func (f *File) Save(transactions []market.Transaction) error {
	if f.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	transactions = Merge(nil, transactions, time.Now().UTC(), f.retention, f.limit)
	temporary := f.path + ".tmp"
	backup := f.path + ".bak"
	if err := write(temporary, document{Version: formatVersion, SavedAt: time.Now().UTC(), Transactions: transactions}); err != nil {
		return err
	}
	_ = os.Remove(backup)
	if err := os.Rename(f.path, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(temporary)
		return fmt.Errorf("rotate state file: %w", err)
	}
	if err := os.Rename(temporary, f.path); err != nil {
		_ = os.Rename(backup, f.path)
		return fmt.Errorf("install state file: %w", err)
	}
	_ = os.Remove(backup)
	return nil
}

func Merge(existing, incoming []market.Transaction, now time.Time, retention time.Duration, limit int) []market.Transaction {
	cutoff := now.Add(-retention)
	byKey := make(map[string]market.Transaction, len(existing)+len(incoming))
	for _, transaction := range append(append([]market.Transaction(nil), existing...), incoming...) {
		transaction = market.NormalizeTransaction(transaction)
		if transaction.TotalPrice <= 0 || transaction.SoldAt.Before(cutoff) || transaction.SoldAt.After(now.Add(5*time.Minute)) {
			continue
		}
		key := transaction.Fingerprint + "/" + transaction.SoldAt.UTC().Format(time.RFC3339Nano)
		byKey[key] = transaction
	}
	out := make([]market.Transaction, 0, len(byKey))
	for _, transaction := range byKey {
		out = append(out, transaction)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SoldAt.After(out[j].SoldAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func read(path string) ([]market.Transaction, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	compressed, err := gzip.NewReader(io.LimitReader(file, 256<<20))
	if err != nil {
		return nil, fmt.Errorf("open state archive: %w", err)
	}
	defer compressed.Close()
	var value document
	decoder := json.NewDecoder(io.LimitReader(compressed, 512<<20))
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode state archive: %w", err)
	}
	if value.Version != formatVersion {
		return nil, fmt.Errorf("unsupported state format %d", value.Version)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("state archive contains trailing data")
	}
	return value.Transactions, nil
}

func write(path string, value document) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create temporary state file: %w", err)
	}
	compressed := gzip.NewWriter(file)
	encodeErr := json.NewEncoder(compressed).Encode(value)
	closeGzipErr := compressed.Close()
	syncErr := file.Sync()
	closeFileErr := file.Close()
	if encodeErr != nil {
		return fmt.Errorf("encode state archive: %w", encodeErr)
	}
	if closeGzipErr != nil {
		return fmt.Errorf("finish state archive: %w", closeGzipErr)
	}
	if syncErr != nil {
		return fmt.Errorf("sync state archive: %w", syncErr)
	}
	if closeFileErr != nil {
		return fmt.Errorf("close state archive: %w", closeFileErr)
	}
	return nil
}
