package logs

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

var ErrQueueFull = errors.New("ingest queue is full")
var ErrQueueClosed = errors.New("ingest queue is closed")

type BatchConfig struct {
	QueueDepth int
	FlushAt    int
	FlushEvery time.Duration
}

type Batcher struct {
	mu     sync.Mutex
	closed bool
	queue  chan []Entry
	done   chan struct{}
}

func NewBatcher(store Writer, config BatchConfig, onError func(int, error)) *Batcher {
	batch := &Batcher{
		queue: make(chan []Entry, config.QueueDepth),
		done:  make(chan struct{}),
	}
	go batch.run(store, config, onError)
	return batch
}

func (b *Batcher) Append(ctx context.Context, entries []Entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrQueueClosed
	}
	select {
	case b.queue <- slices.Clone(entries):
		return nil
	default:
		return ErrQueueFull
	}
}

func (b *Batcher) Close() {
	b.mu.Lock()
	if !b.closed {
		b.closed = true
		close(b.queue)
	}
	b.mu.Unlock()
	<-b.done
}

func (b *Batcher) run(store Writer, config BatchConfig, onError func(int, error)) {
	defer close(b.done)
	ticker := time.NewTicker(config.FlushEvery)
	defer ticker.Stop()

	var pending []Entry
	flush := func() {
		if len(pending) == 0 {
			return
		}
		if err := store.Append(context.Background(), pending); err != nil {
			onError(len(pending), err)
		}
		pending = nil
	}

	for {
		select {
		case entries, open := <-b.queue:
			if !open {
				flush()
				return
			}
			pending = append(pending, entries...)
			if len(pending) >= config.FlushAt {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}
