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
		HDHomeRunAddress: func() string { return "192.168.0.103" }, RecordingsDir: root, Pool: pool,
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

func TestLiveStopReleasesTunerBeforeBufferCleanup(t *testing.T) {
	root := t.TempDir()
	pool := NewTunerPool(1)
	manager := NewLiveManager(context.Background(), LiveConfig{
		HDHomeRunAddress: func() string { return "192.168.0.103" }, RecordingsDir: root, Pool: pool,
		Profile: Profile{Mode: "software"}, ReadyTimeout: time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	manager.newProcess = func(command Command, _ io.Writer) process {
		return &fakeProcess{playlistPath: command.Args[len(command.Args)-1], stopped: make(chan struct{})}
	}
	cleanupStarted := make(chan struct{})
	allowCleanup := make(chan struct{})
	manager.removeAll = func(path string) error {
		close(cleanupStarted)
		<-allowCleanup
		return os.RemoveAll(path)
	}
	defer close(allowCleanup)

	session, err := manager.Start(context.Background(), "7.1", "Live News")
	if err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- manager.Stop(context.Background(), session.ID) }()
	select {
	case <-cleanupStarted:
	case <-time.After(time.Second):
		t.Fatal("live buffer cleanup did not start")
	}
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("stopping live TV waited for buffer cleanup")
	}
	if used, total := pool.Usage(); used != 0 || total != 1 {
		t.Fatalf("unexpected tuner usage after stop: %d of %d", used, total)
	}
}

func TestLiveSessionStopsAfterMediaBecomesIdle(t *testing.T) {
	root := t.TempDir()
	pool := NewTunerPool(1)
	manager := NewLiveManager(context.Background(), LiveConfig{
		HDHomeRunAddress: func() string { return "192.168.0.103" }, RecordingsDir: root, Pool: pool,
		Profile: Profile{Mode: "software"}, ReadyTimeout: time.Second, IdleTimeout: 50 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	manager.newProcess = func(command Command, _ io.Writer) process {
		return &fakeProcess{playlistPath: command.Args[len(command.Args)-1], stopped: make(chan struct{})}
	}
	if _, err := manager.Start(context.Background(), "7.1", "Live News"); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() { manager.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("idle live session did not stop")
	}
	if used, total := pool.Usage(); used != 0 || total != 1 {
		t.Fatalf("idle session retained tuner: %d of %d", used, total)
	}
}

func TestLiveSessionMediaActivityRenewsIdleTimeout(t *testing.T) {
	root := t.TempDir()
	pool := NewTunerPool(1)
	manager := NewLiveManager(context.Background(), LiveConfig{
		HDHomeRunAddress: func() string { return "192.168.0.103" }, RecordingsDir: root, Pool: pool,
		Profile: Profile{Mode: "software"}, ReadyTimeout: time.Second, IdleTimeout: 150 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	manager.newProcess = func(command Command, _ io.Writer) process {
		return &fakeProcess{playlistPath: command.Args[len(command.Args)-1], stopped: make(chan struct{})}
	}
	session, err := manager.Start(context.Background(), "7.1", "Live News")
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(75 * time.Millisecond)
	manager.Touch(session.ID)
	time.Sleep(100 * time.Millisecond)
	if used, total := pool.Usage(); used != 1 || total != 1 {
		t.Fatalf("media activity did not renew the live session: %d of %d", used, total)
	}

	done := make(chan struct{})
	go func() { manager.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("live session did not stop after renewed idle timeout")
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
