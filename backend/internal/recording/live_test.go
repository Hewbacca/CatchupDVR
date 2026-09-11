package recording

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hewbacca/CatchupDVR/backend/internal/model"
)

func TestLiveSessionCreatesAndRemovesTemporaryBuffer(t *testing.T) {
	root := t.TempDir()
	pool := NewTunerPool(2)
	manager := NewLiveManager(context.Background(), LiveConfig{
		HDHomeRunIP: "192.168.0.103", RecordingsDir: root, Pool: pool,
		Profile: Profile{Mode: "software"}, ReadyTimeout: time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	manager.newProcess = func(command Command, _ io.Writer) process {
		return &fakeProcess{playlistPath: command.Args[len(command.Args)-1], stopped: make(chan struct{})}
	}
	session, err := manager.Start(context.Background(), "7.1", "Live News")
	if err != nil {
		t.Fatal(err)
	}
	playlist := filepath.Join(root, filepath.FromSlash(session.PlaylistPath))
	if _, err := os.Stat(playlist); err != nil {
		t.Fatalf("live playlist was not created: %v", err)
	}
	used, total := pool.Usage()
	if used != 1 || total != 2 {
		t.Fatalf("unexpected live tuner usage: %d of %d", used, total)
	}
	if err := manager.Stop(context.Background(), session.ID); err != nil {
		t.Fatal(err)
	}
	manager.Wait()
	if _, err := os.Stat(filepath.Dir(playlist)); !os.IsNotExist(err) {
		t.Fatalf("temporary live buffer still exists: %v", err)
	}
	used, _ = pool.Usage()
	if used != 0 {
		t.Fatalf("live tuner was not released: %d", used)
	}
}

func TestRecordingFolderUsesTitleAndLocalAiringTime(t *testing.T) {
	recording := model.Recording{
		Title: "The Matrix!", ProgramStart: time.Date(2026, 9, 10, 18, 0, 0, 0, time.Local),
	}
	if got := recordingFolderName(recording); got != "the-matrix-09-10-2026-0600pm" {
		t.Fatalf("got %q", got)
	}
}
