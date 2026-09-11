package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Hewbacca/CatchupDVR/backend/internal/config"
	"github.com/Hewbacca/CatchupDVR/backend/internal/guide"
	"github.com/Hewbacca/CatchupDVR/backend/internal/model"
	"github.com/Hewbacca/CatchupDVR/backend/internal/recording"
	"github.com/Hewbacca/CatchupDVR/backend/internal/store"
)

type Store interface {
	Guide(context.Context, time.Time, time.Time) (model.Guide, error)
	Schedule(context.Context, string, int, int, int) (model.Recording, error)
	Recordings(context.Context) ([]model.Recording, error)
	Ping(context.Context) error
}

type RecordingController interface {
	Delete(context.Context, int64) error
}

type Server struct {
	config   config.Config
	store    Store
	refresh  guide.RefreshService
	logger   *slog.Logger
	recorder RecordingController
}

func New(cfg config.Config, store Store, refresh guide.RefreshService, recorder RecordingController, logger *slog.Logger) http.Handler {
	server := &Server{config: cfg, store: store, refresh: refresh, recorder: recorder, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", server.health)
	mux.HandleFunc("GET /api/diagnostics", server.diagnostics)
	mux.HandleFunc("GET /api/guide", server.getGuide)
	mux.HandleFunc("POST /api/admin/guide/refresh", server.refreshGuide)
	mux.HandleFunc("GET /api/recordings", server.getRecordings)
	mux.HandleFunc("POST /api/recordings", server.createRecording)
	mux.HandleFunc("DELETE /api/recordings/{id}", server.deleteRecording)
	mux.Handle("/recordings/", http.StripPrefix("/recordings/", noCacheHLS(http.FileServer(http.Dir(cfg.RecordingsDir)))))
	if info, err := os.Stat(cfg.WebDir); err == nil && info.IsDir() {
		mux.Handle("/", spaHandler(cfg.WebDir))
	}
	return logging(logger, mux)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) diagnostics(w http.ResponseWriter, r *http.Request) {
	device, deviceErr := recording.DetectRenderDevice("/dev/dri", "/sys/class/drm", s.config.GPURenderDevice)
	response := map[string]any{
		"database": "ok", "tunerCount": s.config.TunerCount, "hdHomeRunConfigured": s.config.HDHomeRunIP != "",
		"recordingsDir": s.config.RecordingsDir, "gpuMode": s.config.GPUMode, "renderDevice": device,
		"recordingEngine": "enabled",
	}
	if deviceErr != nil {
		response["gpuWarning"] = deviceErr.Error()
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) getGuide(w http.ResponseWriter, r *http.Request) {
	from := parseTime(r.URL.Query().Get("from"), time.Now().Add(-time.Hour))
	to := parseTime(r.URL.Query().Get("to"), from.Add(6*time.Hour))
	if to.Sub(from) > 48*time.Hour {
		writeError(w, http.StatusBadRequest, "guide window cannot exceed 48 hours")
		return
	}
	result, err := s.store.Guide(r.Context(), from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read guide")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) refreshGuide(w http.ResponseWriter, r *http.Request) {
	var request struct{ Source, Location string }
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if request.Source == "" {
		request.Source = "hdhomerun"
	}
	if request.Location == "" {
		if request.Source == "hdhomerun" {
			request.Location = s.config.HDHomeRunIP
		} else {
			request.Location = s.config.XMLTVFallback
		}
	}
	channels, programs, err := s.refresh.Refresh(r.Context(), request.Source, request.Location)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"channels": channels, "programs": programs})
}

func (s *Server) getRecordings(w http.ResponseWriter, r *http.Request) {
	recordings, err := s.store.Recordings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read recordings")
		return
	}
	writeJSON(w, http.StatusOK, recordings)
}

func (s *Server) createRecording(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ProgramID   string `json:"programId"`
		PreMinutes  *int   `json:"preMinutes"`
		PostMinutes *int   `json:"postMinutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.ProgramID == "" {
		writeError(w, http.StatusBadRequest, "programId is required")
		return
	}
	pre, post := s.config.PrePadding, s.config.PostPadding
	if request.PreMinutes != nil {
		pre = *request.PreMinutes
	}
	if request.PostMinutes != nil {
		post = *request.PostMinutes
	}
	result, err := s.store.Schedule(r.Context(), request.ProgramID, pre, post, s.config.TunerCount)
	if errors.Is(err, store.ErrTunerConflict) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) deleteRecording(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid recording id")
		return
	}
	if err := s.recorder.Delete(r.Context(), id); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseTime(value string, fallback time.Time) time.Time {
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed
	}
	return fallback
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func logging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started))
	})
}

func noCacheHLS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".m3u8") {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		} else if strings.HasSuffix(r.URL.Path, ".ts") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			w.Header().Set("Content-Type", "video/mp2t")
		}
		next.ServeHTTP(w, r)
	})
}

func spaHandler(root string) http.Handler {
	files := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if clean == "." {
			clean = "index.html"
		}
		if _, err := os.Stat(filepath.Join(root, clean)); err == nil {
			if contentType := mime.TypeByExtension(filepath.Ext(clean)); contentType != "" {
				w.Header().Set("Content-Type", contentType)
			}
			files.ServeHTTP(w, r)
			return
		}
		if _, err := os.Stat(filepath.Join(root, "index.html")); err != nil && !errors.Is(err, fs.ErrNotExist) {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("web root: %v", err))
			return
		}
		http.ServeFile(w, r, filepath.Join(root, "index.html"))
	})
}
