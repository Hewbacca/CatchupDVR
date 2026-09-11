package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"path"
	"strings"
	"sync"
	"time"
)

// Cast links are bearer capabilities for a single HLS playlist and the media
// files next to it. They deliberately live only in memory: restarting the
// service invalidates every outstanding link.
const castIdleTimeout = 2 * time.Minute

type castService struct {
	mu       sync.Mutex
	live     LiveController
	sessions map[string]*castSession
	now      func() time.Time
	timeout  time.Duration
}

type castSession struct {
	playlistPath  string
	liveSessionID string
	deadline      time.Time
	timer         *time.Timer
}

func newCastService(live LiveController) *castService {
	return &castService{
		live: live, sessions: make(map[string]*castSession), now: time.Now, timeout: castIdleTimeout,
	}
}

func (c *castService) issue(playlistPath, liveSessionID string) (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(bytes)
	deadline := c.now().Add(c.timeout)
	session := &castSession{playlistPath: playlistPath, liveSessionID: liveSessionID, deadline: deadline}

	c.mu.Lock()
	c.sessions[token] = session
	session.timer = time.AfterFunc(c.timeout, func() { c.expire(token) })
	c.mu.Unlock()
	return token, nil
}

// authorize renews a Cast link whenever the receiver reads an HLS playlist or
// segment. Once the TV stops requesting media, a live session is stopped after
// the short grace period so it cannot leave a tuner reserved indefinitely.
func (c *castService) authorize(token, requestedPath string) bool {
	requestedPath, ok := cleanCastPath(requestedPath)
	if !ok {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	session := c.sessions[token]
	if session == nil || !castAssetAllowed(session.playlistPath, requestedPath) {
		return false
	}
	session.deadline = c.now().Add(c.timeout)
	if session.timer != nil {
		session.timer.Reset(c.timeout)
	}
	return true
}

func (c *castService) revokeLive(liveSessionID string) {
	if liveSessionID == "" {
		return
	}
	c.mu.Lock()
	for token, session := range c.sessions {
		if session.liveSessionID != liveSessionID {
			continue
		}
		if session.timer != nil {
			session.timer.Stop()
		}
		delete(c.sessions, token)
	}
	c.mu.Unlock()
}

func (c *castService) expire(token string) {
	c.mu.Lock()
	session := c.sessions[token]
	if session == nil {
		c.mu.Unlock()
		return
	}
	remaining := time.Until(session.deadline)
	if remaining > 0 {
		session.timer.Reset(remaining)
		c.mu.Unlock()
		return
	}
	delete(c.sessions, token)
	c.mu.Unlock()

	if session.liveSessionID == "" || c.live == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = c.live.Stop(ctx, session.liveSessionID)
	}()
}

func cleanCastPath(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return "", false
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != value {
		return "", false
	}
	return clean, true
}

func castAssetAllowed(playlistPath, requestedPath string) bool {
	playlistPath, playlistOK := cleanCastPath(playlistPath)
	if !playlistOK {
		return false
	}
	return requestedPath == playlistPath || path.Dir(requestedPath) == path.Dir(playlistPath)
}
