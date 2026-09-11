package recording

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hewbacca/CatchupDVR/backend/internal/model"
)

func TestFinalizePlaylistAddsEndListOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.m3u8")
	data := "#EXTM3U\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXTINF:4.0,\nsegment-000000000.ts\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := FinalizePlaylist(path); err != nil {
		t.Fatal(err)
	}
	if err := FinalizePlaylist(path); err != nil {
		t.Fatal(err)
	}
	final, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(final), "#EXT-X-ENDLIST") != 1 {
		t.Fatalf("unexpected final playlist:\n%s", final)
	}
}

func TestSupervisorCapturesAndCompletesDueRecording(t *testing.T) {
	now := time.Now().UTC()
	repository := &fakeRepository{finished: make(chan finishResult, 1), due: &model.Recording{
		ID: 7, ChannelNumber: "7.1", Title: "The Matrix", Status: "scheduled",
		ProgramStart:   time.Date(2026, 9, 10, 18, 0, 0, 0, time.Local),
		ScheduledStart: now.Add(-time.Minute), ScheduledEnd: now.Add(40 * time.Millisecond),
	}}
	supervisor := NewSupervisor(repository, SupervisorConfig{
		HDHomeRunAddress: func() string { return "192.168.0.103" }, RecordingsDir: t.TempDir(),
		Profile: Profile{Mode: "software"}, PollInterval: 5 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	supervisor.newProcess = func(command Command, _ io.Writer) process {
		return &fakeProcess{playlistPath: command.Args[len(command.Args)-1], stopped: make(chan struct{})}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { supervisor.Run(ctx); close(done) }()
	select {
	case result := <-repository.finished:
		if result.status != "completed" || result.message != "" {
			t.Fatalf("unexpected finish: %#v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("recording did not finish")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("supervisor did not stop")
	}
	if repository.playlist != "the-matrix-09-10-2026-0600pm/index.m3u8" || repository.pid != 4321 {
		t.Fatalf("runtime metadata not persisted: path=%q pid=%d", repository.playlist, repository.pid)
	}
}

func TestDeleteStopsActiveRecordingAndRemovesOutput(t *testing.T) {
	now := time.Now().UTC()
	repository := &fakeRepository{started: make(chan struct{}, 1), finished: make(chan finishResult, 1), due: &model.Recording{
		ID: 9, ChannelNumber: "9.1", Title: "Delete Me", Status: "scheduled",
		ProgramStart:   time.Date(2026, 9, 10, 19, 30, 0, 0, time.Local),
		ScheduledStart: now.Add(-time.Minute), ScheduledEnd: now.Add(time.Hour),
	}}
	root := t.TempDir()
	supervisor := NewSupervisor(repository, SupervisorConfig{
		HDHomeRunAddress: func() string { return "192.168.0.103" }, RecordingsDir: root,
		Profile: Profile{Mode: "software"}, PollInterval: 5 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	supervisor.newProcess = func(command Command, _ io.Writer) process {
		return &fakeProcess{playlistPath: command.Args[len(command.Args)-1], stopped: make(chan struct{})}
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { supervisor.Run(ctx); close(done) }()
	select {
	case <-repository.started:
	case <-time.After(time.Second):
		t.Fatal("recording did not start")
	}
	if err := supervisor.Delete(context.Background(), 9); err != nil {
		t.Fatal(err)
	}
	if !repository.deleted {
		t.Fatal("recording row was not deleted")
	}
	if _, err := os.Stat(filepath.Join(root, "delete-me-09-10-2026-0730pm")); !os.IsNotExist(err) {
		t.Fatalf("recording output still exists: %v", err)
	}
	stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("supervisor did not stop")
	}
}

type finishResult struct{ status, message string }

type fakeRepository struct {
	mutex    sync.Mutex
	due      *model.Recording
	playlist string
	pid      int
	finished chan finishResult
	started  chan struct{}
	deleted  bool
}

func (r *fakeRepository) ClaimDueRecording(context.Context, time.Time) (*model.Recording, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.due == nil {
		return nil, nil
	}
	result := *r.due
	r.due = nil
	return &result, nil
}

func (r *fakeRepository) InterruptedRecordings(context.Context) ([]model.Recording, error) {
	return nil, nil
}

func (r *fakeRepository) SetRecordingProcess(_ context.Context, _ int64, playlist string, pid int, _ time.Time) error {
	r.playlist, r.pid = playlist, pid
	if r.started != nil {
		r.started <- struct{}{}
	}
	return nil
}

func (r *fakeRepository) TouchRecording(context.Context, int64, time.Time) error { return nil }

func (r *fakeRepository) FinishRecording(_ context.Context, _ int64, status, message string, _ time.Time) error {
	r.finished <- finishResult{status: status, message: message}
	return nil
}

func (r *fakeRepository) RequeueRecording(context.Context, int64, string) error { return nil }

func (r *fakeRepository) CancelRecording(_ context.Context, id int64, _ time.Time) (model.Recording, error) {
	return model.Recording{ID: id, Status: "cancelled", PlaylistPath: r.playlist}, nil
}

func (r *fakeRepository) DeleteRecording(context.Context, int64) error {
	r.deleted = true
	return nil
}

type fakeProcess struct {
	playlistPath string
	stopped      chan struct{}
	once         sync.Once
}

func (p *fakeProcess) Start() error {
	if err := os.MkdirAll(filepath.Dir(p.playlistPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p.playlistPath, []byte("#EXTM3U\n#EXTINF:4.0,\nsegment-000000000.ts\n"), 0o644)
}

func (p *fakeProcess) Wait() error            { <-p.stopped; return nil }
func (p *fakeProcess) Signal(os.Signal) error { p.once.Do(func() { close(p.stopped) }); return nil }
func (p *fakeProcess) Kill() error            { p.once.Do(func() { close(p.stopped) }); return nil }
func (p *fakeProcess) PID() int               { return 4321 }
