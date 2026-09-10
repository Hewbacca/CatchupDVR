package recording

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBuildCommandPreservesChasePlayContract(t *testing.T) {
	command := BuildCommand(Profile{Mode: "vaapi", RenderDevice: "/dev/dri/renderD129", Deinterlace: true}, "http://tuner:5004/auto/v7.1", "/recordings/42")
	joined := strings.Join(command.Args, " ")
	for _, required := range []string{"-hls_playlist_type event", "-hls_time 4", "append_list+independent_segments+program_date_time+temp_file", "deinterlace_vaapi", "h264_vaapi", "segment-%09d.ts"} {
		if !strings.Contains(joined, required) {
			t.Errorf("missing %q in %s", required, joined)
		}
	}
	if slices.Contains(command.Args, "delete_segments") {
		t.Fatal("EVENT recording must not delete old segments")
	}
}

func TestHDHomeRunStreamURL(t *testing.T) {
	streamURL, err := HDHomeRunStreamURL("192.0.2.20", "7.1")
	if err != nil {
		t.Fatal(err)
	}
	if streamURL != "http://192.0.2.20:5004/auto/v7.1" {
		t.Fatalf("got %s", streamURL)
	}
}

func TestDetectRenderDevicePrefersIntel(t *testing.T) {
	root := t.TempDir()
	dri := filepath.Join(root, "dev", "dri")
	sys := filepath.Join(root, "sys", "class", "drm")
	for _, name := range []string{"renderD128", "renderD129"} {
		if err := os.MkdirAll(filepath.Join(sys, name, "device"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dri, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dri, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sys, "renderD128", "device", "vendor"), []byte("0x1002\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sys, "renderD129", "device", "vendor"), []byte("0x8086\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	device, err := DetectRenderDevice(dri, sys, "")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(device) != "renderD129" {
		t.Fatalf("got %s", device)
	}
}
