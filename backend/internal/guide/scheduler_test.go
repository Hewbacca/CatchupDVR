package guide

import (
	"testing"
	"time"
)

func TestNextRefreshDelayStaysInsideWindow(t *testing.T) {
	for range 1000 {
		delay := NextRefreshDelay()
		if delay < 20*time.Hour || delay > 28*time.Hour {
			t.Fatalf("delay outside 20–28 hours: %s", delay)
		}
	}
}
