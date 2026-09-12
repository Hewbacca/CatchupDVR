package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hewbacca/CatchupDVR/backend/internal/config"
	"github.com/Hewbacca/CatchupDVR/backend/internal/guide"
	"github.com/Hewbacca/CatchupDVR/backend/internal/model"
	"github.com/Hewbacca/CatchupDVR/backend/internal/recording"
	storage "github.com/Hewbacca/CatchupDVR/backend/internal/store"
)

type testStore struct {
	credentials  *model.AuthCredentials
	tunerAddress string
}

func (*testStore) Guide(context.Context, time.Time, time.Time) (model.Guide, error) {
	return model.Guide{}, nil
}
func (*testStore) GuideDays(context.Context) ([]string, error) { return []string{"2026-09-10"}, nil }
func (*testStore) SearchGuide(context.Context, string) ([]model.Program, error) {
	return []model.Program{}, nil
}
func (*testStore) Schedule(context.Context, string, int, int, int) (model.Recording, error) {
	return model.Recording{}, nil
}
func (*testStore) Recordings(context.Context) ([]model.Recording, error) { return nil, nil }
func (*testStore) Ping(context.Context) error                            { return nil }
func (s *testStore) AuthCredentials(context.Context) (model.AuthCredentials, bool, error) {
	if s.credentials == nil {
		return model.AuthCredentials{}, false, nil
	}
	return *s.credentials, true, nil
}
func (s *testStore) CreateAuthCredentials(_ context.Context, credentials model.AuthCredentials) error {
	if s.credentials != nil {
		return storage.ErrAuthenticationConfigured
	}
	s.credentials = &credentials
	return nil
}
func (s *testStore) UpdateAuthCredentials(_ context.Context, credentials model.AuthCredentials) error {
	if s.credentials == nil {
		return errors.New("account has not been configured")
	}
	s.credentials = &credentials
	return nil
}
func (s *testStore) HDHomeRunAddress(context.Context) (string, error) { return s.tunerAddress, nil }
func (s *testStore) SetHDHomeRunAddress(_ context.Context, address string) error {
	s.tunerAddress = address
	return nil
}
func (*testStore) FavoriteChannels(context.Context, string) ([]string, error)     { return []string{}, nil }
func (*testStore) SetFavoriteChannel(context.Context, string, string, bool) error { return nil }

type testRecorder struct{}

func (testRecorder) Delete(context.Context, int64) error { return nil }

type testTuners struct{ used, total int }

func (t testTuners) Usage() (int, int) { return t.used, t.total }

type testLive struct {
	startedChannel string
	stoppedID      string
	touchedID      string
}

func (l *testLive) Start(_ context.Context, channel, title string) (recording.LiveSession, error) {
	l.startedChannel = channel
	return recording.LiveSession{ID: "live-1", ChannelNumber: channel, Title: title, PlaylistPath: ".live/live-1/index.m3u8"}, nil
}
func (l *testLive) Stop(_ context.Context, id string) error {
	l.stoppedID = id
	return nil
}
func (l *testLive) Touch(id string) { l.touchedID = id }

func testHandler(t *testing.T, live LiveController, tuners TunerCounter) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(config.Config{RecordingsDir: t.TempDir(), TunerCount: 2}, config.NewTunerAddress(""), &testStore{}, guide.RefreshService{}, testRecorder{}, live, tuners, logger)
}

func setupCookie(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewBufferString(`{"username":"tester","password":"test-password-123"}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("setup failed: status=%d body=%s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one session cookie, got %d", len(cookies))
	}
	return cookies[0]
}

func TestDiagnosticsReportsCurrentTunerAvailability(t *testing.T) {
	live := &testLive{}
	handler := testHandler(t, live, testTuners{used: 1, total: 2})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/diagnostics", nil)
	request.AddCookie(setupCookie(t, handler))
	handler.ServeHTTP(response, request)
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
	cookie := setupCookie(t, handler)
	request.AddCookie(cookie)
	handler.ServeHTTP(response, request)
	var session recording.LiveSession
	if err := json.NewDecoder(response.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusCreated || live.startedChannel != "7.1" || session.PlaylistPath != ".live/live-1/index.m3u8" {
		t.Fatalf("unexpected live start: status=%d channel=%q session=%+v", response.Code, live.startedChannel, session)
	}
	response = httptest.NewRecorder()
	stopRequest := httptest.NewRequest(http.MethodDelete, "/api/live/live-1", nil)
	stopRequest.AddCookie(cookie)
	handler.ServeHTTP(response, stopRequest)
	if response.Code != http.StatusNoContent || live.stoppedID != "live-1" {
		t.Fatalf("unexpected live stop: status=%d id=%q", response.Code, live.stoppedID)
	}
}

func TestLiveMediaReadsTouchSession(t *testing.T) {
	root := t.TempDir()
	playlist := filepath.Join(root, ".live", "live-1", "index.m3u8")
	if err := os.MkdirAll(filepath.Dir(playlist), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(playlist, []byte("#EXTM3U\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	live := &testLive{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := New(config.Config{RecordingsDir: root, TunerCount: 2}, config.NewTunerAddress(""), &testStore{}, guide.RefreshService{}, testRecorder{}, live, testTuners{total: 2}, logger)
	cookie := setupCookie(t, handler)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/recordings/.live/live-1/index.m3u8", nil)
	request.AddCookie(cookie)
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || live.touchedID != "live-1" {
		t.Fatalf("expected live playlist read to retain session: status=%d touched=%q", response.Code, live.touchedID)
	}
}

func TestCastLiveMediaReadTouchesSession(t *testing.T) {
	root := t.TempDir()
	playlist := filepath.Join(root, ".live", "live-1", "index.m3u8")
	if err := os.MkdirAll(filepath.Dir(playlist), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(playlist, []byte("#EXTM3U\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	live := &testLive{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := New(config.Config{RecordingsDir: root, TunerCount: 2}, config.NewTunerAddress(""), &testStore{}, guide.RefreshService{}, testRecorder{}, live, testTuners{total: 2}, logger)
	cookie := setupCookie(t, handler)

	createResponse := httptest.NewRecorder()
	createRequest := httptest.NewRequest(http.MethodPost, "/api/cast", bytes.NewBufferString(`{"playlistPath":".live/live-1/index.m3u8","liveSessionId":"live-1"}`))
	createRequest.Header.Set("Content-Type", "application/json")
	createRequest.AddCookie(cookie)
	handler.ServeHTTP(createResponse, createRequest)
	var media struct {
		Path string `json:"path"`
	}
	if createResponse.Code != http.StatusCreated || json.NewDecoder(createResponse.Body).Decode(&media) != nil || media.Path == "" {
		t.Fatalf("could not create Cast media: status=%d body=%s", createResponse.Code, createResponse.Body.String())
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, media.Path, nil))
	if response.Code != http.StatusOK || live.touchedID != "live-1" {
		t.Fatalf("expected Cast live playlist read to retain session: status=%d touched=%q", response.Code, live.touchedID)
	}
}

func TestAccountSetupLoginAndAPIProtection(t *testing.T) {
	handler := testHandler(t, &testLive{}, testTuners{total: 2})

	status := httptest.NewRecorder()
	handler.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/auth/status", nil))
	if status.Code != http.StatusOK || !bytes.Contains(status.Body.Bytes(), []byte(`"setupRequired":true`)) {
		t.Fatalf("unexpected setup status: %d %s", status.Code, status.Body.String())
	}

	protected := httptest.NewRecorder()
	handler.ServeHTTP(protected, httptest.NewRequest(http.MethodGet, "/api/diagnostics", nil))
	if protected.Code != http.StatusUnauthorized {
		t.Fatalf("expected protected API to reject anonymous request, got %d", protected.Code)
	}

	cookie := setupCookie(t, handler)
	allowed := httptest.NewRecorder()
	allowedRequest := httptest.NewRequest(http.MethodGet, "/api/diagnostics", nil)
	allowedRequest.AddCookie(cookie)
	handler.ServeHTTP(allowed, allowedRequest)
	if allowed.Code != http.StatusOK {
		t.Fatalf("expected authenticated API access, got %d", allowed.Code)
	}

	logout := httptest.NewRecorder()
	logoutRequest := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	logoutRequest.AddCookie(cookie)
	handler.ServeHTTP(logout, logoutRequest)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("unexpected logout status: %d", logout.Code)
	}

	badLogin := httptest.NewRecorder()
	badLoginRequest := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(`{"username":"tester","password":"wrong-password"}`))
	badLoginRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(badLogin, badLoginRequest)
	if badLogin.Code != http.StatusUnauthorized {
		t.Fatalf("expected bad login rejection, got %d", badLogin.Code)
	}

	login := httptest.NewRecorder()
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(`{"username":"tester","password":"test-password-123"}`))
	loginRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(login, loginRequest)
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
		t.Fatalf("expected successful login, got %d %s", login.Code, login.Body.String())
	}
}
