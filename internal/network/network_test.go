package network

import (
	"bytes"
	"testing"
	"time"
)

func TestProtocolRoundTripAndLimit(t *testing.T) {
	input := map[string]any{"signature": "minecraft:elytra", "price": 123}
	b, err := Encode(P1, MsgListingObserved, input)
	if err != nil {
		t.Fatal(err)
	}
	f, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if f.Priority != P1 || f.Type != MsgListingObserved || !bytes.Contains(f.Payload, []byte("elytra")) {
		t.Fatalf("bad frame %+v", f)
	}
	_, err = Encode(P2, MsgChat, bytes.Repeat([]byte{'x'}, MaxFrameSize+1))
	if err == nil {
		t.Fatal("oversize payload accepted")
	}
}
func TestPriorityQueueChatFloodCannotDelayP0(t *testing.T) {
	q := NewPriorityQueue(10)
	for i := 0; i < 10; i++ {
		_ = q.Push(P2, []byte("chat"))
	}
	if err := q.Push(P0, []byte("invalidate")); err != nil {
		t.Fatal(err)
	}
	b, p, ok := q.TryPop()
	if !ok || p != P0 || string(b) != "invalidate" {
		t.Fatalf("urgent frame delayed: p=%v b=%s", p, b)
	}
	size, dropped := q.Stats()
	if size != 9 || dropped != 1 {
		t.Fatalf("size=%d dropped=%d", size, dropped)
	}
}

func TestPriorityQueueSignalsWaitingWriter(t *testing.T) {
	q := NewPriorityQueue(2)
	select {
	case <-q.Wake():
		t.Fatal("empty queue signaled")
	default:
	}
	if err := q.Push(P1, []byte("value")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-q.Wake():
	case <-time.After(time.Second):
		t.Fatal("queue did not wake writer")
	}
}
func TestSchedulerSpreadsAssignments(t *testing.T) {
	now := time.Now()
	workers := []WorkerState{{WorkerID: "a", Online: true, AvailableBalance: 1000, InventoryCapacity: 1, SuccessRateBPS: 8000, LastHeartbeat: now}, {WorkerID: "b", Online: true, AvailableBalance: 1000, InventoryCapacity: 1, SuccessRateBPS: 7000, LastHeartbeat: now}}
	targets := []SearchTarget{{ID: "x", ExpectedProfit: 1000, DesiredRedundancy: 1}, {ID: "y", ExpectedProfit: 900, DesiredRedundancy: 1}}
	a := Schedule(workers, targets, now)
	if len(a) != 2 {
		t.Fatalf("expected 2 assignments, got %d", len(a))
	}
	if a[0].WorkerID == a[1].WorkerID || a[0].Target.ID == a[1].Target.ID {
		t.Fatalf("assignments not spread: %+v", a)
	}
}
func TestSchedulerRejectsStaleWorker(t *testing.T) {
	now := time.Now()
	a := Schedule([]WorkerState{{WorkerID: "stale", Online: true, AvailableBalance: 100, InventoryCapacity: 1, LastHeartbeat: now.Add(-time.Minute)}}, []SearchTarget{{ID: "x"}}, now)
	if len(a) != 0 {
		t.Fatal("stale worker assigned")
	}
}
