package guide

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Fetcher struct {
	Client    *http.Client
	CachePath string
}

type discovery struct {
	DeviceAuth string `json:"DeviceAuth"`
}

func (f Fetcher) HDHomeRun(ctx context.Context, host string) ([]byte, error) {
	host = strings.TrimRight(strings.TrimSpace(host), "/")
	if host == "" {
		return nil, fmt.Errorf("HDHomeRun address is not configured")
	}
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "http://" + host
	}
	data, err := f.fetch(ctx, host+"/discover.json")
	if err != nil {
		return f.cached(fmt.Errorf("discover HDHomeRun: %w", err))
	}
	var device discovery
	if err := json.Unmarshal(data, &device); err != nil || device.DeviceAuth == "" {
		return f.cached(fmt.Errorf("discover response has no DeviceAuth"))
	}
	guideURL := "https://api.hdhomerun.com/api/xmltv?DeviceAuth=" + url.QueryEscape(device.DeviceAuth)
	data, err = f.fetch(ctx, guideURL)
	if err != nil {
		return f.cached(fmt.Errorf("download HDHomeRun guide: %w", err))
	}
	if err := f.saveCache(data); err != nil {
		return nil, err
	}
	return data, nil
}

func (f Fetcher) URL(ctx context.Context, location string) ([]byte, error) {
	data, err := f.fetch(ctx, location)
	if err != nil {
		return f.cached(err)
	}
	if err := f.saveCache(data); err != nil {
		return nil, err
	}
	return data, nil
}

func (f Fetcher) File(location string) ([]byte, error) {
	return os.ReadFile(location)
}

func (f Fetcher) fetch(ctx context.Context, location string) ([]byte, error) {
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept-Encoding", "gzip")
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%s returned %s", location, response.Status)
	}
	var body io.Reader = response.Body
	if strings.EqualFold(response.Header.Get("Content-Encoding"), "gzip") {
		compressed, err := gzip.NewReader(response.Body)
		if err != nil {
			return nil, err
		}
		defer compressed.Close()
		body = compressed
	}
	return io.ReadAll(io.LimitReader(body, 128<<20))
}

func (f Fetcher) saveCache(data []byte) error {
	if f.CachePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(f.CachePath), 0o755); err != nil {
		return err
	}
	temporary := f.CachePath + ".tmp"
	if err := os.WriteFile(temporary, data, 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, f.CachePath)
}

func (f Fetcher) cached(cause error) ([]byte, error) {
	if f.CachePath == "" {
		return nil, cause
	}
	data, err := os.ReadFile(f.CachePath)
	if err != nil {
		return nil, cause
	}
	return data, nil
}
