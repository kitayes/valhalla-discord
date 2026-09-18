package web

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
)

// SSEMessage represents an individual event sent through Server-Sent Events.
type SSEMessage struct {
	Event string `json:"event"`
	Data  string `json:"data"`
}

type sseSubscriber struct {
	id     uint64
	userID int64
	ch     chan SSEMessage
}

// SSEBroker manages real-time Server-Sent Events connections.
type SSEBroker struct {
	mu          sync.RWMutex
	subscribers map[uint64]*sseSubscriber
	nextID      atomic.Uint64
}

// NewSSEBroker creates and initializes a new SSE broker.
func NewSSEBroker() *SSEBroker {
	return &SSEBroker{
		subscribers: make(map[uint64]*sseSubscriber),
	}
}

// Subscribe registers a new subscriber channel. The returned cleanup func unregisters it.
func (b *SSEBroker) Subscribe(ctx context.Context, userID int64) (<-chan SSEMessage, func()) {
	subID := b.nextID.Add(1)
	ch := make(chan SSEMessage, 32)
	sub := &sseSubscriber{id: subID, userID: userID, ch: ch}

	b.mu.Lock()
	b.subscribers[subID] = sub
	b.mu.Unlock()

	cleanup := func() {
		b.mu.Lock()
		if s, ok := b.subscribers[subID]; ok {
			delete(b.subscribers, subID)
			close(s.ch)
		}
		b.mu.Unlock()
	}

	return ch, cleanup
}

// Broadcast sends an event and payload to all connected clients.
func (b *SSEBroker) Broadcast(eventName string, payload any) {
	dataBytes, err := json.Marshal(payload)
	if err != nil {
		dataBytes = []byte("{}")
	}
	msg := SSEMessage{
		Event: eventName,
		Data:  string(dataBytes),
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, sub := range b.subscribers {
		select {
		case sub.ch <- msg:
		default:
			// Non-blocking write: avoid stalling fast broadcasts on slow network connections
		}
	}
}

// BroadcastToUser sends an event to all active sessions of a specific user.
func (b *SSEBroker) BroadcastToUser(userID int64, eventName string, payload any) {
	dataBytes, err := json.Marshal(payload)
	if err != nil {
		dataBytes = []byte("{}")
	}
	msg := SSEMessage{
		Event: eventName,
		Data:  string(dataBytes),
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, sub := range b.subscribers {
		if sub.userID == userID {
			select {
			case sub.ch <- msg:
			default:
			}
		}
	}
}
