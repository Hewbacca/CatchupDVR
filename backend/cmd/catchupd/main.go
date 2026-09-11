package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Hewbacca/CatchupDVR/backend/internal/config"
	"github.com/Hewbacca/CatchupDVR/backend/internal/guide"
	"github.com/Hewbacca/CatchupDVR/backend/internal/httpapi"
	"github.com/Hewbacca/CatchupDVR/backend/internal/recording"
	"github.com/Hewbacca/CatchupDVR/backend/internal/store"
)

func main() {
	cfg := config.FromEnv()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	database, err := store.Open(cfg.DatabasePath)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer database.Close()
	refresh := guide.RefreshService{
		Fetcher: guide.Fetcher{CachePath: filepath.Join(filepath.Dir(cfg.DatabasePath), "guide-last-good.xml")},
		Store:   database,
	}
	guideSource, guideLocation := "hdhomerun", cfg.HDHomeRunIP
	if guideLocation == "" && cfg.XMLTVFallback != "" {
		guideSource, guideLocation = "url", cfg.XMLTVFallback
		if _, err := os.Stat(cfg.XMLTVFallback); err == nil {
			guideSource = "file"
		}
	}
	go guide.RunScheduler(ctx, refresh, guideSource, guideLocation, logger)
	renderDevice := cfg.GPURenderDevice
	if cfg.GPUMode != "software" && renderDevice == "" {
		if detected, err := recording.DetectRenderDevice("/dev/dri", "/sys/class/drm", ""); err == nil {
			renderDevice = detected
		} else if cfg.GPUMode == "auto" {
			logger.Warn("no render device detected; recordings will use software encoding", "error", err)
		}
	}
	recorder := recording.NewSupervisor(database, recording.SupervisorConfig{
		HDHomeRunIP:   cfg.HDHomeRunIP,
		RecordingsDir: cfg.RecordingsDir,
		Profile: recording.Profile{
			FFmpegPath:   cfg.FFmpegPath,
			Mode:         cfg.GPUMode,
			RenderDevice: renderDevice,
			Deinterlace:  cfg.Deinterlace,
		},
	}, logger)
	recorderDone := make(chan struct{})
	go func() {
		recorder.Run(ctx)
		close(recorderDone)
	}()
	server := &http.Server{Addr: cfg.HTTPAddr, Handler: httpapi.New(cfg, database, refresh, recorder, logger)}
	logger.Info("CatchUp DVR listening", "address", cfg.HTTPAddr)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	serverErr := server.ListenAndServe()
	stop()
	<-recorderDone
	if serverErr != nil && serverErr != http.ErrServerClosed {
		logger.Error("server stopped", "error", serverErr)
		os.Exit(1)
	}
}
