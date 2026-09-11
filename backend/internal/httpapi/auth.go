package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"github.com/Hewbacca/CatchupDVR/backend/internal/model"
	"github.com/Hewbacca/CatchupDVR/backend/internal/store"
)

const (
	sessionCookieName = "catchup_session"
	sessionLifetime   = 30 * 24 * time.Hour
)

type authenticationContextKey struct{}

type authService struct {
	store Store

	mu          sync.RWMutex
	loaded      bool
	credentials model.AuthCredentials
	configured  bool
}

func newAuthService(database Store) *authService { return &authService{store: database} }

func (a *authService) getCredentials(ctx context.Context) (model.AuthCredentials, bool, error) {
	a.mu.RLock()
	if a.loaded {
		credentials, configured := a.credentials, a.configured
		a.mu.RUnlock()
		return credentials, configured, nil
	}
	a.mu.RUnlock()

	credentials, configured, err := a.store.AuthCredentials(ctx)
	if err != nil {
		return model.AuthCredentials{}, false, err
	}
	a.mu.Lock()
	if !a.loaded {
		a.credentials, a.configured, a.loaded = credentials, configured, true
	}
	credentials, configured = a.credentials, a.configured
	a.mu.Unlock()
	return credentials, configured, nil
}

func (a *authService) setup(ctx context.Context, username, password string) (model.AuthCredentials, error) {
	username = strings.TrimSpace(username)
	if err := validateAccount(username, password); err != nil {
		return model.AuthCredentials{}, err
	}
	_, configured, err := a.getCredentials(ctx)
	if err != nil {
		return model.AuthCredentials{}, err
	}
	if configured {
		return model.AuthCredentials{}, store.ErrAuthenticationConfigured
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return model.AuthCredentials{}, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return model.AuthCredentials{}, err
	}
	credentials := model.AuthCredentials{Username: username, PasswordHash: string(hash), SessionSecret: secret, CreatedAt: time.Now().UTC()}
	if err := a.store.CreateAuthCredentials(ctx, credentials); err != nil {
		return model.AuthCredentials{}, err
	}
	a.mu.Lock()
	a.credentials, a.configured, a.loaded = credentials, true, true
	a.mu.Unlock()
	return credentials, nil
}

func (a *authService) login(ctx context.Context, username, password string) (model.AuthCredentials, bool, error) {
	credentials, configured, err := a.getCredentials(ctx)
	if err != nil || !configured {
		return model.AuthCredentials{}, false, err
	}
	validUsername := subtle.ConstantTimeCompare([]byte(credentials.Username), []byte(strings.TrimSpace(username))) == 1
	validPassword := bcrypt.CompareHashAndPassword([]byte(credentials.PasswordHash), []byte(password)) == nil
	if !validUsername || !validPassword {
		return model.AuthCredentials{}, false, nil
	}
	return credentials, true, nil
}

func (a *authService) changePassword(ctx context.Context, currentPassword, newPassword string) (model.AuthCredentials, bool, error) {
	if err := validatePassword(newPassword); err != nil {
		return model.AuthCredentials{}, false, err
	}
	credentials, configured, err := a.getCredentials(ctx)
	if err != nil || !configured {
		return model.AuthCredentials{}, false, err
	}
	if bcrypt.CompareHashAndPassword([]byte(credentials.PasswordHash), []byte(currentPassword)) != nil {
		return model.AuthCredentials{}, false, nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return model.AuthCredentials{}, false, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return model.AuthCredentials{}, false, err
	}
	credentials.PasswordHash, credentials.SessionSecret = string(hash), secret
	if err := a.store.UpdateAuthCredentials(ctx, credentials); err != nil {
		return model.AuthCredentials{}, false, err
	}
	a.mu.Lock()
	a.credentials = credentials
	a.mu.Unlock()
	return credentials, true, nil
}

func (a *authService) sessionUser(ctx context.Context, cookie *http.Cookie) (string, bool, error) {
	credentials, configured, err := a.getCredentials(ctx)
	if err != nil || !configured || cookie == nil {
		return "", false, err
	}
	return verifySession(cookie.Value, credentials)
}

func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	_, configured, err := s.auth.getCredentials(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read account settings")
		return
	}
	_, authenticated, err := s.auth.sessionUser(r.Context(), sessionCookie(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not verify session")
		return
	}
	address, err := s.store.HDHomeRunAddress(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read tuner settings")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"setupRequired": !configured, "authenticated": authenticated, "tunerSetupRequired": s.tunerSetupRequired(configured, address)})
}

func (s *Server) setupAccount(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}
	credentials, err := s.auth.setup(r.Context(), request.Username, request.Password)
	if errors.Is(err, store.ErrAuthenticationConfigured) {
		writeError(w, http.StatusConflict, "an account already exists; please sign in")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.setSession(w, r, credentials)
	writeJSON(w, http.StatusCreated, map[string]bool{"setupRequired": false, "authenticated": true, "tunerSetupRequired": s.tunerSetupRequired(true, "")})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}
	credentials, valid, err := s.auth.login(r.Context(), request.Username, request.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not sign in")
		return
	}
	if !valid {
		writeError(w, http.StatusUnauthorized, "incorrect username or password")
		return
	}
	s.setSession(w, r, credentials)
	address, err := s.store.HDHomeRunAddress(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read tuner settings")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"setupRequired": false, "authenticated": true, "tunerSetupRequired": s.tunerSetupRequired(true, address)})
}

func (s *Server) tunerSetupRequired(accountConfigured bool, address string) bool {
	return accountConfigured && strings.TrimSpace(address) == "" && strings.TrimSpace(s.config.XMLTVFallback) == ""
}

func (s *Server) testTunerConnection(w http.ResponseWriter, r *http.Request) {
	address, err := s.verifyTunerConnection(r)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "address": address})
}

func (s *Server) configureTuner(w http.ResponseWriter, r *http.Request) {
	address, err := s.verifyTunerConnection(r)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if err := s.store.SetHDHomeRunAddress(r.Context(), address); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save tuner settings")
		return
	}
	s.tuner.SetAddress(address)
	go s.refreshGuideAfterTunerSetup(address)
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "address": address})
}

func (s *Server) verifyTunerConnection(r *http.Request) (string, error) {
	var request struct {
		Address string `json:"address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		return "", errors.New("HDHomeRun address is required")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	return s.refresh.Fetcher.TestHDHomeRun(ctx, request.Address)
}

func (s *Server) refreshGuideAfterTunerSetup(address string) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	channels, programs, err := s.refresh.Refresh(ctx, "hdhomerun", address)
	if err != nil {
		s.logger.Warn("initial guide refresh failed; it will retry automatically", "error", err)
		return
	}
	s.logger.Info("initial guide refreshed", "channels", channels, "programs", programs)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secureCookie(r)})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var request struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "current and new passwords are required")
		return
	}
	credentials, changed, err := s.auth.changePassword(r.Context(), request.CurrentPassword, request.NewPassword)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !changed {
		writeError(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}
	s.setSession(w, r, credentials)
	writeJSON(w, http.StatusOK, map[string]bool{"changed": true})
}

// authentication leaves the application shell and account endpoints public so
// a new install can display setup. Every API endpoint and recording asset is
// otherwise protected, including HLS playlists and individual segments.
func (s *Server) authentication(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicAuthenticationRoute(r.URL.Path) || (!strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/recordings/")) || (r.URL.Path == "/api/health" && isLoopbackRequest(r)) {
			next.ServeHTTP(w, r)
			return
		}
		username, valid, err := s.auth.sessionUser(r.Context(), sessionCookie(r))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not verify session")
			return
		}
		if !valid {
			writeError(w, http.StatusUnauthorized, "sign in required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authenticationContextKey{}, username)))
	})
}

func authenticatedUsername(r *http.Request) (string, bool) {
	username, ok := r.Context().Value(authenticationContextKey{}).(string)
	return username, ok && username != ""
}

func publicAuthenticationRoute(path string) bool {
	return path == "/api/auth/status" || path == "/api/auth/setup" || path == "/api/auth/login" || path == "/api/auth/logout"
}

func sessionCookie(r *http.Request) *http.Cookie {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return nil
	}
	return cookie
}

func (s *Server) setSession(w http.ResponseWriter, r *http.Request, credentials model.AuthCredentials) {
	expiresAt := time.Now().Add(sessionLifetime)
	payload := base64.RawURLEncoding.EncodeToString([]byte(credentials.Username + "\x00" + strconv.FormatInt(expiresAt.Unix(), 10)))
	mac := hmac.New(sha256.New, credentials.SessionSecret)
	_, _ = mac.Write([]byte(payload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: payload + "." + signature, Path: "/", Expires: expiresAt, MaxAge: int(sessionLifetime.Seconds()), HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secureCookie(r)})
}

func verifySession(value string, credentials model.AuthCredentials) (string, bool, error) {
	payload, signature, ok := strings.Cut(value, ".")
	if !ok || payload == "" || signature == "" {
		return "", false, nil
	}
	sentSignature, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return "", false, nil
	}
	mac := hmac.New(sha256.New, credentials.SessionSecret)
	_, _ = mac.Write([]byte(payload))
	if !hmac.Equal(sentSignature, mac.Sum(nil)) {
		return "", false, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", false, nil
	}
	username, expiry, ok := strings.Cut(string(decoded), "\x00")
	if !ok || subtle.ConstantTimeCompare([]byte(username), []byte(credentials.Username)) != 1 {
		return "", false, nil
	}
	expiresAt, err := strconv.ParseInt(expiry, 10, 64)
	if err != nil || time.Now().Unix() >= expiresAt {
		return "", false, nil
	}
	return username, true, nil
}

func validateAccount(username, password string) error {
	if username == "" || len(username) > 128 || !utf8.ValidString(username) || strings.ContainsAny(username, "\r\n\x00") {
		return errors.New("choose a username between 1 and 128 characters")
	}
	return validatePassword(password)
}

func validatePassword(password string) error {
	if len(password) < 10 {
		return errors.New("choose a password with at least 10 characters")
	}
	if len(password) > 256 {
		return errors.New("password must be 256 characters or fewer")
	}
	return nil
}

func secureCookie(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	forwardedProto := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])
	return strings.EqualFold(forwardedProto, "https")
}

func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
