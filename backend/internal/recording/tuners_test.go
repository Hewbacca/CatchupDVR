package recording

import (
	"context"
	"testing"
	"time"
)

func TestRecordingPreemptsTemporaryLiveTuner(t *testing.T) {
	pool := NewTunerPool(1)
	liveDone := make(chan struct{})
	preempted := make(chan struct{}, 1)
	if !pool.AcquireLive("live:1", func() {
		preempted <- struct{}{}
		pool.Release("live:1")
		close(liveDone)
	}, liveDone) {
		t.Fatal("could not reserve live tuner")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !pool.AcquireRecording(ctx, "recording:1") {
		t.Fatal("recording did not acquire preempted tuner")
	}
	select {
	case <-preempted:
	default:
		t.Fatal("live session was not preempted")
	}
	used, total := pool.Usage()
	if used != 1 || total != 1 {
		t.Fatalf("unexpected usage: %d of %d", used, total)
	}
}
