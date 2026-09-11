package recording

import (
	"context"
	"sync"
)

type tunerReservation struct {
	preempt func()
	done    <-chan struct{}
}

// TunerPool coordinates persistent recordings and temporary live-TV buffers.
// Recordings may preempt live buffers so a scheduled recording is never missed.
type TunerPool struct {
	mu     sync.Mutex
	total  int
	active map[string]tunerReservation
}

func NewTunerPool(total int) *TunerPool {
	if total < 1 {
		total = 1
	}
	return &TunerPool{total: total, active: make(map[string]tunerReservation)}
}

func (p *TunerPool) AcquireLive(key string, preempt func(), done <-chan struct{}) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.active[key]; exists {
		return true
	}
	if len(p.active) >= p.total {
		return false
	}
	p.active[key] = tunerReservation{preempt: preempt, done: done}
	return true
}

func (p *TunerPool) AcquireRecording(ctx context.Context, key string) bool {
	for {
		p.mu.Lock()
		if _, exists := p.active[key]; exists {
			p.mu.Unlock()
			return true
		}
		if len(p.active) < p.total {
			p.active[key] = tunerReservation{}
			p.mu.Unlock()
			return true
		}
		var preempt func()
		var done <-chan struct{}
		for _, reservation := range p.active {
			if reservation.preempt != nil {
				preempt, done = reservation.preempt, reservation.done
				break
			}
		}
		p.mu.Unlock()
		if preempt == nil {
			return false
		}
		preempt()
		select {
		case <-done:
		case <-ctx.Done():
			return false
		}
	}
}

func (p *TunerPool) Release(key string) {
	p.mu.Lock()
	delete(p.active, key)
	p.mu.Unlock()
}

func (p *TunerPool) Usage() (used, total int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.active), p.total
}
