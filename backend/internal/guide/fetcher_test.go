package guide

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHDHomeRunDiscoversFreshAuthAndReadsGzip(t *testing.T) {
	var paths []string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.String())
		if strings.HasSuffix(r.URL.Path, "/discover.json") {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"DeviceAuth":"rotating token"}`)), Header: make(http.Header)}, nil
		}
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		_, _ = writer.Write([]byte("<tv></tv>"))
		_ = writer.Close()
		header := make(http.Header)
		header.Set("Content-Encoding", "gzip")
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(compressed.Bytes())), Header: header}, nil
	})}
	fetcher := Fetcher{Client: client}
	for range 2 {
		data, err := fetcher.HDHomeRun(context.Background(), "192.0.2.10")
		if err != nil || string(data) != "<tv></tv>" {
			t.Fatalf("data=%q err=%v", data, err)
		}
	}
	if len(paths) != 4 || !strings.Contains(paths[1], "DeviceAuth=rotating+token") || !strings.HasSuffix(paths[2], "/discover.json") {
		t.Fatalf("unexpected request order: %#v", paths)
	}
}

func TestFetchErrorRedactsAuthorizationQuery(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("User-Agent"); !strings.HasPrefix(got, "CatchUpDVR/") {
			t.Fatalf("unexpected user agent %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Status:     "403 Forbidden",
			Body:       io.NopCloser(strings.NewReader("forbidden")),
			Header:     make(http.Header),
		}, nil
	})}
	_, err := (Fetcher{Client: client}).fetch(context.Background(), "https://api.hdhomerun.com/api/xmltv?DeviceAuth=do-not-log-this")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "do-not-log-this") || strings.Contains(err.Error(), "DeviceAuth") {
		t.Fatalf("authorization query leaked in error: %v", err)
	}
	if !strings.Contains(err.Error(), "403 Forbidden") {
		t.Fatalf("status missing from error: %v", err)
	}
}
