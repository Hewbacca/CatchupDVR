package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Hewbacca/CatchupDVR/backend/internal/config"
	"github.com/Hewbacca/CatchupDVR/backend/internal/guide"
	"github.com/Hewbacca/CatchupDVR/backend/internal/model"
	"github.com/Hewbacca/CatchupDVR/backend/internal/recording"
)

type testStore struct{}

func (testStore) Guide(context.Context, time.Time, time.Time) (model.Guide, error) {
	return model.Guide{}, nil
}
func (testStore) Schedule(context.Context, string, int, int, int) (model.Recording, error) {
	return model.Recording{}, nil
}
func (testStore) Recordings(context.Context) ([]model.Recording, error) { return nil, nil }
func (testStore) Ping(context.Context) error                            { return nil }

type testRecorder struct{}

func (testRecorder) Delete(context.Context, int64) error { return nil }

type testTuners struct{ used, total int }

func (t testTuners) Usage() (int, int) { return t.used, t.total }

type testLive struct {
	startedChannel string
	stoppedID      string
}

func (l *testLive) Start(_ context.Context, channel, title string) (recording.LiveSession, error) {
	l.startedChannel = channel
	return recording.LiveSession{ID: "live-1", ChannelNumber: channel, Title: title, PlaylistPath: ".live/live-1/index.m3u8"}, nil
}
func (l *testLive) Stop(_ context.Context, id string) error {
	l.stoppedID = id
	return nil
}

func testHandler(t *testing.T, live LiveController, tuners TunerCounter) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(config.Config{RecordingsDir: t.TempDir(), TunerCount: 2}, testStore{}, guide.RefreshService{}, testRecorder{}, live, tuners, logger)
}

func TestDiagnosticsReportsCurrentTunerAvailability(t *testing.T) {
	live := &testLive{}
	handler := testHandler(t, live, testTuners{used: 1, total: 2})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/diagnostics", nil))
	var body struct {
		TunerCount      int `json:"tunerCount"`
		TunersInUse     int `json:"tunersInUse"`
		TunersAvailable int `json:"tunersAvailable"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.TunerCount != 2 || body.TunersInUse != 1 || body.TunersAvailable != 1 {
		t.Fatalf("unexpected tuner status: %+v", body)
	}
}

func TestLiveSessionLifecycleRoutes(t *testing.T) {
	live := &testLive{}
	handler := testHandler(t, live, testTuners{total: 2})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/live", bytes.NewBufferString(`{"channelNumber":"7.1","title":"Live News"}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	var session recording.LiveSession
	if err := json.NewDecoder(response.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusCreated || live.startedChannel != "7.1" || session.PlaylistPath != ".live/live-1/index.m3u8" {
		t.Fatalf("unexpected live start: status=%d channel=%q session=%+v", response.Code, live.startedChannel, session)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/live/live-1", nil))
	if response.Code != http.StatusNoContent || live.stoppedID != "live-1" {
		t.Fatalf("unexpected live stop: status=%d id=%q", response.Code, live.stoppedID)
	}
}
