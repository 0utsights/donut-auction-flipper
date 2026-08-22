package network

import (
	"errors"
	"sync"
)

var ErrQueueFull = errors.New("priority queue full")

type PriorityQueue struct {
	mu        sync.Mutex
	wake      chan struct{}
	closed    bool
	capacity  int
	size      int
	queues    [3][][]byte
	droppedP2 uint64
}

func NewPriorityQueue(capacity int) *PriorityQueue {
	return &PriorityQueue{capacity: capacity, wake: make(chan struct{}, 1)}
}
func (q *PriorityQueue) Push(priority Priority, data []byte) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return errors.New("queue closed")
	}
	if q.size >= q.capacity {
		if priority == P2 {
			q.droppedP2++
			return ErrQueueFull
		}
		if len(q.queues[P2]) > 0 {
			q.queues[P2] = q.queues[P2][1:]
			q.droppedP2++
			q.size--
		} else {
			return ErrQueueFull
		}
	}
	q.queues[priority] = append(q.queues[priority], data)
	q.size++
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return nil
}
func (q *PriorityQueue) TryPop() ([]byte, Priority, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for p := P0; p <= P2; p++ {
		if len(q.queues[p]) > 0 {
			v := q.queues[p][0]
			q.queues[p] = q.queues[p][1:]
			q.size--
			return v, p, true
		}
	}
	return nil, 0, false
}
func (q *PriorityQueue) Wake() <-chan struct{} { return q.wake }
func (q *PriorityQueue) Close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}
func (q *PriorityQueue) Stats() (size int, droppedP2 uint64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.size, q.droppedP2
}
