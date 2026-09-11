package guide

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"time"
)

const (
	minRefreshDelay = 20 * time.Hour
	refreshWindow   = 8 * time.Hour
	retryDelay      = 30 * time.Minute
)

func NextRefreshDelay() time.Duration {
	return minRefreshDelay + time.Duration(rand.Int64N(int64(refreshWindow)+1))
}

func RunScheduler(ctx context.Context, service RefreshService, source, location string, logger *slog.Logger) {
	if location == "" {
		return
	}
	delay := time.Duration(0)
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		channels, programs, err := service.Refresh(ctx, source, location)
		if err != nil {
			logger.Warn("guide refresh failed; retaining last successful guide", "error", err, "retryIn", retryDelay)
			delay = retryDelay
			continue
		}
		delay = NextRefreshDelay()
		logger.Info("guide refreshed", "channels", channels, "programs", programs, "nextRefreshIn", delay)
	}
}

// RunHDHomeRunScheduler reads the saved tuner address before each attempt so a
// first-run setup can enable guide refreshes without restarting the service.
func RunHDHomeRunScheduler(ctx context.Context, service RefreshService, address func() string, logger *slog.Logger) {
	delay := time.Duration(0)
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		location := ""
		if address != nil {
			location = address()
		}
		if location == "" {
			delay = retryDelay
			continue
		}
		channels, programs, err := service.Refresh(ctx, "hdhomerun", location)
		if err != nil {
			logger.Warn("guide refresh failed; retaining last successful guide", "error", err, "retryIn", retryDelay)
			delay = retryDelay
			continue
		}
		delay = NextRefreshDelay()
		logger.Info("guide refreshed", "channels", channels, "programs", programs, "nextRefreshIn", delay)
	}
}
