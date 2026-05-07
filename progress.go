package main

import "sync"

type Progress struct {
	Processed int
	Total     int // -1 while still being determined
	Current   string
}

// Reporter is a small pub-sub used by long-running operations (scan, execute)
// to broadcast progress to any number of SSE subscribers. The publisher must
// never block on a slow consumer, so each subscriber gets its own buffered
// channel and we drop the oldest queued event under back-pressure.
type Reporter struct {
	mu     sync.Mutex
	subs   map[chan Progress]struct{}
	last   Progress
	hasAny bool
	closed bool
}

const reporterBuf = 16

func NewReporter() *Reporter {
	return &Reporter{subs: map[chan Progress]struct{}{}}
}

func (r *Reporter) Publish(p Progress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.last = p
	r.hasAny = true
	for ch := range r.subs {
		send(ch, p)
	}
}

// Subscribe returns a receive channel of progress events plus an unsubscribe
// func. The most recent event (if any) is delivered immediately so a late
// subscriber doesn't have to wait for the next tick.
func (r *Reporter) Subscribe() (<-chan Progress, func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := make(chan Progress, reporterBuf)
	if r.closed {
		close(ch)
		return ch, func() {}
	}
	r.subs[ch] = struct{}{}
	if r.hasAny {
		ch <- r.last
	}
	return ch, func() { r.unsubscribe(ch) }
}

func (r *Reporter) unsubscribe(ch chan Progress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.subs[ch]; !ok {
		return
	}
	delete(r.subs, ch)
	close(ch)
}

// Close signals end-of-stream to all current and future subscribers.
func (r *Reporter) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	for ch := range r.subs {
		close(ch)
		delete(r.subs, ch)
	}
}

// send is non-blocking. If the buffer is full, drop the oldest queued event
// and enqueue the new one — newest progress matters more than completeness.
func send(ch chan Progress, p Progress) {
	for {
		select {
		case ch <- p:
			return
		default:
			select {
			case <-ch:
			default:
				return
			}
		}
	}
}
