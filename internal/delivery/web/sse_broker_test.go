package web

import (
	"context"
	"testing"
	"time"
)

func TestSSEBrokerSubscriptionAndBroadcast(t *testing.T) {
	broker := NewSSEBroker()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch1, cleanup1 := broker.Subscribe(ctx, 100)
	defer cleanup1()

	ch2, cleanup2 := broker.Subscribe(ctx, 200)
	defer cleanup2()

	// Broadcast to all
	broker.Broadcast("match_update", map[string]string{"status": "ready"})

	select {
	case msg := <-ch1:
		if msg.Event != "match_update" {
			t.Errorf("expected event 'match_update', got '%s'", msg.Event)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for broadcast on ch1")
	}

	select {
	case msg := <-ch2:
		if msg.Event != "match_update" {
			t.Errorf("expected event 'match_update', got '%s'", msg.Event)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for broadcast on ch2")
	}

	// Targeted broadcast to user 100 only
	broker.BroadcastToUser(100, "user_ping", map[string]string{"msg": "hello"})

	select {
	case msg := <-ch1:
		if msg.Event != "user_ping" {
			t.Errorf("expected event 'user_ping', got '%s'", msg.Event)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for targeted msg on ch1")
	}

	select {
	case msg := <-ch2:
		t.Fatalf("unexpected message on ch2: %+v", msg)
	default:
		// good: ch2 did not receive user 100 message
	}

	// Unsubscribe ch1
	cleanup1()

	broker.Broadcast("another_event", map[string]string{})

	select {
	case _, ok := <-ch1:
		if ok {
			t.Fatal("expected ch1 to be closed after cleanup")
		}
	default:
	}
}
