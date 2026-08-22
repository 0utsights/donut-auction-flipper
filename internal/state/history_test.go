package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"donut-network/internal/market"
)

func transaction(id string, soldAt time.Time) market.Transaction {
	return market.NormalizeTransaction(market.Transaction{SellerName: id, Item: market.Item{ID: "minecraft:diamond", Quantity: 1}, TotalPrice: 100, SoldAt: soldAt, Source: market.SourceDonutAPI})
}

func TestMergeDeduplicatesExpiresAndLimits(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	a := transaction("a", now.Add(-time.Hour))
	b := transaction("b", now.Add(-2*time.Hour))
	old := transaction("old", now.Add(-40*24*time.Hour))
	merged := Merge([]market.Transaction{a, old}, []market.Transaction{a, b}, now, 31*24*time.Hour, 1)
	if len(merged) != 1 || merged[0].SellerName != "a" {
		t.Fatalf("unexpected merge: %+v", merged)
	}
}

func TestFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json.gz")
	store := NewFile(path, 31*24*time.Hour, 100)
	want := []market.Transaction{transaction("alex", time.Now().UTC().Add(-time.Minute))}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Signature.Exact != "minecraft:diamond" {
		t.Fatalf("unexpected history: %+v", got)
	}
}

func TestLoadRecoversFromValidBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json.gz")
	store := NewFile(path, 31*24*time.Hour, 100)
	want := []market.Transaction{transaction("backup", time.Now().UTC().Add(-time.Minute))}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".bak"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SellerName != "backup" {
		t.Fatalf("backup not recovered: %+v", got)
	}
}
