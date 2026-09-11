package recording

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Hewbacca/CatchupDVR/backend/internal/model"
)

type Repository interface {
	ClaimDueRecording(context.Context, time.Time) (*model.Recording, error)
	InterruptedRecordings(context.Context) ([]model.Recording, error)
	SetRecordingProcess(context.Context, int64, string, int, time.Time) error
	TouchRecording(context.Context, int64, time.Time) error
	FinishRecording(context.Context, int64, string, string, time.Time) error
	RequeueRecording(context.Context, int64, string) error
	CancelRecording(context.Context, int64, time.Time) (model.Recording, error)
	DeleteRecording(context.Context, int64) error
}

type SupervisorConfig struct {
	HDHomeRunAddress func() string
	RecordingsDir    string
	Profile          Profile
	Pool             *TunerPool
	PollInterval     time.Duration
	Heartbeat        time.Duration
}

type process interface {
	Start() error
	Wait() error
	Signal(os.Signal) error
	Kill() error
	PID() int
}

type commandProcess struct{ command *exec.Cmd }

func (p *commandProcess) Start() error                  { return p.command.Start() }
func (p *commandProcess) Wait() error                   { return p.command.Wait() }
func (p *commandProcess) Signal(signal os.Signal) error { return p.command.Process.Signal(signal) }
func (p *commandProcess) Kill() error                   { return p.command.Process.Kill() }
func (p *commandProcess) PID() int                      { return p.command.Process.Pid }

type Supervisor struct {
	repository Repository
	config     SupervisorConfig
	logger     *slog.Logger
	newProcess func(Command, io.Writer) process
	now        func() time.Time
	waitGroup  sync.WaitGroup
	activeMu   sync.Mutex
	active     map[int64]*activeJob
}

type activeJob struct {
	cancel context.CancelCauseFunc
	done   chan struct{}
}

var ErrRecordingDeleted = errors.New("recording deleted by user")

func NewSupervisor(repository Repository, config SupervisorConfig, logger *slog.Logger) *Supervisor {
	if config.HDHomeRunAddress == nil {
		config.HDHomeRunAddress = func() string { return "" }
	}
	if config.PollInterval <= 0 {
		config.PollInterval = time.Second
	}
	if config.Heartbeat <= 0 {
		config.Heartbeat = 10 * time.Second
	}
	if config.Pool == nil {
		config.Pool = NewTunerPool(1)
	}
	return &Supervisor{
		repository: repository,
		config:     config,
		logger:     logger,
		now:        time.Now,
		newProcess: func(command Command, output io.Writer) process {
			cmd := exec.Command(command.Path, command.Args...)
			cmd.Stdout = output
			cmd.Stderr = output
			return &commandProcess{command: cmd}
		},
		active: make(map[int64]*activeJob),
	}
}

// Run blocks until ctx is cancelled and all active FFmpeg processes have exited.
func (s *Supervisor) Run(ctx context.Context) {
	s.recoverInterrupted(ctx)
	s.startDue(ctx)
	ticker := time.NewTicker(s.config.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.waitGroup.Wait()
			return
		case <-ticker.C:
			s.startDue(ctx)
		}
	}
}

func (s *Supervisor) startDue(ctx context.Context) {
	for ctx.Err() == nil {
		s.activeMu.Lock()
		recording, err := s.repository.ClaimDueRecording(ctx, s.now().UTC())
		if err != nil {
			s.activeMu.Unlock()
			s.logger.Error("claim due recording", "error", err)
			return
		}
		if recording == nil {
			s.activeMu.Unlock()
			return
		}
		jobCtx, cancel := context.WithCancelCause(ctx)
		job := &activeJob{cancel: cancel, done: make(chan struct{})}
		s.active[recording.ID] = job
		s.activeMu.Unlock()
		s.waitGroup.Add(1)
		go func() {
			defer s.waitGroup.Done()
			defer func() {
				s.activeMu.Lock()
				delete(s.active, recording.ID)
				close(job.done)
				s.activeMu.Unlock()
			}()
			s.capture(jobCtx, *recording)
		}()
	}
}

// Delete stops an active capture, releases its tuner, and removes all persisted output.
func (s *Supervisor) Delete(ctx context.Context, id int64) error {
	s.activeMu.Lock()
	recording, err := s.repository.CancelRecording(ctx, id, s.now().UTC())
	if err != nil {
		s.activeMu.Unlock()
		return err
	}
	job := s.active[id]
	if job != nil {
		job.cancel(ErrRecordingDeleted)
	}
	s.activeMu.Unlock()
	if job != nil {
		select {
		case <-job.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := os.RemoveAll(recordingOutputDir(s.config.RecordingsDir, recording)); err != nil {
		return fmt.Errorf("remove recording files: %w", err)
	}
	return s.repository.DeleteRecording(ctx, id)
}

func (s *Supervisor) recoverInterrupted(ctx context.Context) {
	recordings, err := s.repository.InterruptedRecordings(ctx)
	if err != nil {
		s.logger.Error("find interrupted recordings", "error", err)
		return
	}
	now := s.now().UTC()
	for _, recording := range recordings {
		playlist := s.absolutePlaylist(recording)
		if !recording.ScheduledEnd.After(now) {
			status, message := "completed", ""
			if err := FinalizePlaylist(playlist); err != nil {
				status, message = "failed", "interrupted recording has no playable output: "+err.Error()
			}
			if err := s.repository.FinishRecording(ctx, recording.ID, status, message, now); err != nil {
				s.logger.Error("finalize interrupted recording", "recording", recording.ID, "error", err)
			}
			continue
		}
		if err := s.repository.RequeueRecording(ctx, recording.ID, "resuming after service restart"); err != nil {
			s.logger.Error("requeue interrupted recording", "recording", recording.ID, "error", err)
		}
	}
}

func (s *Supervisor) capture(ctx context.Context, recording model.Recording) {
	reservationKey := fmt.Sprintf("recording:%d", recording.ID)
	if !s.config.Pool.AcquireRecording(ctx, reservationKey) {
		if errors.Is(context.Cause(ctx), ErrRecordingDeleted) {
			return
		}
		if ctx.Err() != nil {
			persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.repository.RequeueRecording(persistCtx, recording.ID, "paused during service shutdown")
			cancel()
			return
		}
		s.finish(recording.ID, "failed", "no tuner was available when recording started")
		return
	}
	defer s.config.Pool.Release(reservationKey)
	inputURL, err := HDHomeRunStreamURL(s.config.HDHomeRunAddress(), recording.ChannelNumber)
	if err != nil {
		s.finish(recording.ID, "failed", err.Error())
		return
	}
	outputDir := recordingOutputDir(s.config.RecordingsDir, recording)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		s.finish(recording.ID, "failed", "create recording directory: "+err.Error())
		return
	}
	playlistPath, err := filepath.Rel(s.config.RecordingsDir, filepath.Join(outputDir, "index.m3u8"))
	if err != nil {
		s.finish(recording.ID, "failed", "create recording path: "+err.Error())
		return
	}
	playlistPath = filepath.ToSlash(playlistPath)
	command := BuildCommand(s.config.Profile, inputURL, outputDir)
	output := &limitedBuffer{limit: 16 * 1024}
	process := s.newProcess(command, output)
	if err := process.Start(); err != nil {
		s.finish(recording.ID, "failed", "start FFmpeg: "+err.Error())
		return
	}
	now := s.now().UTC()
	persistCtx, cancelPersist := context.WithTimeout(context.Background(), 5*time.Second)
	err = s.repository.SetRecordingProcess(persistCtx, recording.ID, playlistPath, process.PID(), now)
	cancelPersist()
	if err != nil {
		_ = process.Kill()
		_ = process.Wait()
		if errors.Is(context.Cause(ctx), ErrRecordingDeleted) {
			return
		}
		s.finish(recording.ID, "failed", "persist FFmpeg process: "+err.Error())
		return
	}
	s.logger.Info("recording started", "recording", recording.ID, "channel", recording.ChannelNumber, "pid", process.PID())

	waitResult := make(chan error, 1)
	go func() { waitResult <- process.Wait() }()
	heartbeat := time.NewTicker(s.config.Heartbeat)
	defer heartbeat.Stop()
	remaining := recording.ScheduledEnd.Sub(now)
	if remaining < 0 {
		remaining = 0
	}
	endTimer := time.NewTimer(remaining)
	defer endTimer.Stop()

	for {
		select {
		case waitErr := <-waitResult:
			message := "FFmpeg exited before the recording ended"
			if waitErr != nil {
				message += ": " + waitErr.Error()
			}
			if details := strings.TrimSpace(output.String()); details != "" {
				message += "; " + details
			}
			s.finish(recording.ID, "failed", message)
			return
		case <-heartbeat.C:
			if err := s.repository.TouchRecording(ctx, recording.ID, s.now().UTC()); err != nil && ctx.Err() == nil {
				s.logger.Warn("update recording heartbeat", "recording", recording.ID, "error", err)
			}
		case <-endTimer.C:
			s.stopProcess(process, waitResult)
			s.complete(recording.ID, filepath.Join(outputDir, "index.m3u8"), output.String())
			return
		case <-ctx.Done():
			s.stopProcess(process, waitResult)
			if errors.Is(context.Cause(ctx), ErrRecordingDeleted) {
				return
			}
			persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := s.repository.RequeueRecording(persistCtx, recording.ID, "paused during service shutdown")
			cancel()
			if err != nil {
				s.logger.Error("requeue recording during shutdown", "recording", recording.ID, "error", err)
			}
			return
		}
	}
}

func (s *Supervisor) stopProcess(process process, waitResult <-chan error) {
	_ = process.Signal(os.Interrupt)
	select {
	case <-waitResult:
		return
	case <-time.After(7 * time.Second):
		_ = process.Kill()
		select {
		case <-waitResult:
		case <-time.After(time.Second):
		}
	}
}

func (s *Supervisor) complete(id int64, playlistPath, processOutput string) {
	if err := FinalizePlaylist(playlistPath); err != nil {
		message := "recording produced no playable output: " + err.Error()
		if details := strings.TrimSpace(processOutput); details != "" {
			message += "; " + details
		}
		s.finish(id, "failed", message)
		return
	}
	s.finish(id, "completed", "")
}

func (s *Supervisor) finish(id int64, status, message string) {
	if len(message) > 4000 {
		message = message[len(message)-4000:]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.repository.FinishRecording(ctx, id, status, message, s.now().UTC()); err != nil {
		s.logger.Error("finish recording", "recording", id, "status", status, "error", err)
		return
	}
	s.logger.Info("recording finished", "recording", id, "status", status, "error", message)
}

func (s *Supervisor) absolutePlaylist(recording model.Recording) string {
	if recording.PlaylistPath != "" {
		return filepath.Join(s.config.RecordingsDir, filepath.FromSlash(recording.PlaylistPath))
	}
	return filepath.Join(recordingOutputDir(s.config.RecordingsDir, recording), "index.m3u8")
}

// FinalizePlaylist makes an interrupted HLS EVENT recording a finite, playable asset.
func FinalizePlaylist(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Contains(data, []byte("#EXTINF:")) {
		return errors.New("playlist contains no media segments")
	}
	if bytes.Contains(data, []byte("#EXT-X-ENDLIST")) {
		return nil
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	if len(data) > 0 && data[len(data)-1] != '\n' {
		if _, err := file.WriteString("\n"); err != nil {
			return err
		}
	}
	_, err = file.WriteString("#EXT-X-ENDLIST\n")
	return err
}

type limitedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	originalLength := len(data)
	if originalLength >= b.limit {
		b.buffer.Reset()
		_, _ = b.buffer.Write(data[originalLength-b.limit:])
		return originalLength, nil
	}
	if excess := b.buffer.Len() + originalLength - b.limit; excess > 0 {
		current := b.buffer.Bytes()
		remaining := append([]byte(nil), current[excess:]...)
		b.buffer.Reset()
		_, _ = b.buffer.Write(remaining)
	}
	_, _ = b.buffer.Write(data)
	return originalLength, nil
}

func (b *limitedBuffer) String() string { return b.buffer.String() }
