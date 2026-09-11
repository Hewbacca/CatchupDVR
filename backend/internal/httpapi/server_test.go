package httpapi

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordingsHandlerAddsStartHintToPlaylist(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "7")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	playlist := "#EXTM3U\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXTINF:4.0,\nsegment-000000000.ts\n"
	if err := os.WriteFile(filepath.Join(directory, "index.m3u8"), []byte(playlist), 0o644); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/7/index.m3u8", nil)
	response := httptest.NewRecorder()
	recordingsHandler(root).ServeHTTP(response, request)
	result := response.Result()
	defer result.Body.Close()
	body, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	if result.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("#EXT-X-START:TIME-OFFSET=0,PRECISE=YES")) {
		t.Fatalf("status=%d playlist=%s", result.StatusCode, body)
	}
	if result.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected cache control %q", result.Header.Get("Cache-Control"))
	}
}

func TestPlaylistStartHintIsNotDuplicated(t *testing.T) {
	playlist := []byte("#EXTM3U\n#EXT-X-START:TIME-OFFSET=0,PRECISE=YES\n#EXTINF:4.0,\na.ts\n")
	result := playlistWithStartHint(playlist)
	if strings.Count(string(result), "#EXT-X-START:") != 1 {
		t.Fatalf("duplicate start hint: %s", result)
	}
}
