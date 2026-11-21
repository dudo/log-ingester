package logs

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type writerFunc func(context.Context, []Entry) error

func (f writerFunc) Append(ctx context.Context, entries []Entry) error {
	return f(ctx, entries)
}

func TestBatcherCoalescesAndDrains(t *testing.T) {
	var writes [][]Entry
	store := writerFunc(func(_ context.Context, entries []Entry) error {
		writes = append(writes, entries)
		return nil
	})
	batch := NewBatcher(store, BatchConfig{QueueDepth: 2, FlushAt: 32, FlushEvery: time.Hour}, func(_ int, err error) {
		t.Error(err)
	})
	t.Cleanup(batch.Close)
	entries := []Entry{{Message: "first"}, {Message: "second"}}
	for _, entry := range entries {
		if err := batch.Append(t.Context(), []Entry{entry}); err != nil {
			t.Fatal(err)
		}
	}
	batch.Close()

	if !reflect.DeepEqual(writes, [][]Entry{entries}) {
		t.Fatalf("writes = %+v, want one combined write", writes)
	}
	if err := batch.Append(t.Context(), entries); !errors.Is(err, ErrQueueClosed) {
		t.Fatalf("append after close = %v, want ErrQueueClosed", err)
	}
}

func TestBatcherRejectsWhenQueueIsFull(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	store := writerFunc(func(_ context.Context, _ []Entry) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return nil
	})
	batch := NewBatcher(store, BatchConfig{QueueDepth: 1, FlushAt: 1, FlushEvery: time.Hour}, func(_ int, err error) {
		t.Error(err)
	})
	t.Cleanup(batch.Close)
	t.Cleanup(func() { close(release) })

	entries := []Entry{{Message: "queued"}}
	if err := batch.Append(t.Context(), entries); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not reach the sink")
	}
	if err := batch.Append(t.Context(), entries); err != nil {
		t.Fatal(err)
	}
	if err := batch.Append(t.Context(), entries); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("append while full = %v, want ErrQueueFull", err)
	}
}
