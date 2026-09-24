package capture

import (
	"context"
	"sync/atomic"
	"time"
)

// Recorder queues exchanges and writes them in batches, off the request
// path: a slow disk drops captures instead of slowing traffic.
type Recorder struct {
	store   *Store
	queue   chan Exchange
	Dropped atomic.Int64
	LastErr atomic.Value // error
}

// NewRecorder writes into store once Run starts.
func NewRecorder(store *Store) *Recorder {
	return &Recorder{store: store, queue: make(chan Exchange, 1024)}
}

// Record queues e without blocking.
func (r *Recorder) Record(e Exchange) {
	select {
	case r.queue <- e:
	default:
		r.Dropped.Add(1)
	}
}

// Run writes queued exchanges until ctx ends, then writes what is left.
func (r *Recorder) Run(ctx context.Context) {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	var batch []Exchange
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := r.store.Add(context.Background(), batch); err != nil {
			r.LastErr.Store(err)
		}
		batch = batch[:0]
	}
	for {
		select {
		case e := <-r.queue:
			batch = append(batch, e)
			if len(batch) >= 100 {
				flush()
			}
		case <-tick.C:
			flush()
		case <-ctx.Done():
			for {
				select {
				case e := <-r.queue:
					batch = append(batch, e)
				default:
					flush()
					return
				}
			}
		}
	}
}
