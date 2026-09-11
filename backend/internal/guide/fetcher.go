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
	host, device, err := f.discover(ctx, host)
	if err != nil {
		return f.cached(err)
	}
	guideURL := "https://api.hdhomerun.com/api/xmltv?DeviceAuth=" + url.QueryEscape(device.DeviceAuth)
	data, err := f.fetch(ctx, guideURL)
	if err != nil {
		return f.cached(fmt.Errorf("download HDHomeRun guide: %w", err))
	}
	if err := f.saveCache(data); err != nil {
		return nil, err
	}
	return data, nil
}

// TestHDHomeRun confirms that the supplied address reaches an HDHomeRun and
// exposes the DeviceAuth value needed for guide downloads. It intentionally
// stops at discovery so a working tuner is not rejected because an unrelated
// guide-provider request is temporarily unavailable.
func (f Fetcher) TestHDHomeRun(ctx context.Context, address string) (string, error) {
	host, _, err := f.discover(ctx, address)
	return host, err
}

func (f Fetcher) discover(ctx context.Context, address string) (string, discovery, error) {
	host, err := NormalizeHDHomeRunAddress(address)
	if err != nil {
		return "", discovery{}, err
	}
	data, err := f.fetch(ctx, "http://"+host+"/discover.json")
	if err != nil {
		return "", discovery{}, fmt.Errorf("discover HDHomeRun: %w", err)
	}
	var device discovery
	if err := json.Unmarshal(data, &device); err != nil || device.DeviceAuth == "" {
		return "", discovery{}, fmt.Errorf("discover response has no DeviceAuth")
	}
	return host, device, nil
}

// NormalizeHDHomeRunAddress accepts a bare IPv4 address or hostname such as
// hdhomerun.local. Schemes, paths, credentials, and ports are intentionally
// excluded: CatchUp always reaches the tuner through its standard endpoints.
func NormalizeHDHomeRunAddress(address string) (string, error) {
	address = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(address)), ".")
	if address == "" || len(address) > 253 || strings.ContainsAny(address, ":/?#@[]\\") {
		return "", fmt.Errorf("enter an HDHomeRun IP address or hostname")
	}
	for _, label := range strings.Split(address, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("enter an HDHomeRun IP address or hostname")
		}
		for _, character := range label {
			if !(character >= 'a' && character <= 'z') && !(character >= '0' && character <= '9') && character != '-' {
				return "", fmt.Errorf("enter an HDHomeRun IP address or hostname")
			}
		}
	}
	return address, nil
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
	req.Header.Set("User-Agent", "CatchUpDVR/0.1 (+https://github.com/Hewbacca/CatchupDVR)")
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%s returned %s", redactedLocation(location), response.Status)
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

func redactedLocation(location string) string {
	parsed, err := url.Parse(location)
	if err != nil {
		return "remote guide service"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
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
