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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var ErrNoTuners = errors.New("all tuners are currently in use")

type LiveSession struct {
	ID            string `json:"id"`
	ChannelNumber string `json:"channelNumber"`
	Title         string `json:"title"`
	PlaylistPath  string `json:"playlistPath"`
}

type LiveConfig struct {
	HDHomeRunAddress func() string
	RecordingsDir    string
	Profile          Profile
	Pool             *TunerPool
	ReadyTimeout     time.Duration
	MaxDuration      time.Duration
}

type LiveManager struct {
	rootCtx    context.Context
	config     LiveConfig
	logger     *slog.Logger
	newProcess func(Command, io.Writer) process
	removeAll  func(string) error
	mu         sync.Mutex
	jobs       map[string]*liveJob
	waitGroup  sync.WaitGroup
	sequence   atomic.Uint64
}

type liveJob struct {
	session    LiveSession
	key        string
	dir        string
	process    process
	output     *limitedBuffer
	cancel     context.CancelFunc
	done       chan struct{}
	waitResult chan error
}

func NewLiveManager(ctx context.Context, config LiveConfig, logger *slog.Logger) *LiveManager {
	if config.HDHomeRunAddress == nil {
		config.HDHomeRunAddress = func() string { return "" }
	}
	if config.Pool == nil {
		config.Pool = NewTunerPool(1)
	}
	if config.ReadyTimeout <= 0 {
		config.ReadyTimeout = 20 * time.Second
	}
	if config.MaxDuration <= 0 {
		config.MaxDuration = 6 * time.Hour
	}
	liveRoot := filepath.Join(config.RecordingsDir, ".live")
	if err := os.RemoveAll(liveRoot); err != nil {
		logger.Warn("remove stale live buffers", "error", err)
	}
	return &LiveManager{
		rootCtx: ctx,
		config:  config,
		logger:  logger,
		newProcess: func(command Command, output io.Writer) process {
			cmd := exec.Command(command.Path, command.Args...)
			cmd.Stdout = output
			cmd.Stderr = output
			return &commandProcess{command: cmd}
		},
		removeAll: os.RemoveAll,
		jobs:      make(map[string]*liveJob),
	}
}

func (m *LiveManager) Start(ctx context.Context, channelNumber, title string) (LiveSession, error) {
	inputURL, err := HDHomeRunStreamURL(m.config.HDHomeRunAddress(), channelNumber)
	if err != nil {
		return LiveSession{}, err
	}
	id := strconv.FormatInt(time.Now().UTC().UnixNano(), 36) + "-" + strconv.FormatUint(m.sequence.Add(1), 36)
	relativeDir := filepath.Join(".live", id)
	outputDir := filepath.Join(m.config.RecordingsDir, relativeDir)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return LiveSession{}, fmt.Errorf("create live buffer: %w", err)
	}
	jobCtx, cancel := context.WithCancel(m.rootCtx)
	job := &liveJob{
		session: LiveSession{ID: id, ChannelNumber: channelNumber, Title: title, PlaylistPath: filepath.ToSlash(filepath.Join(relativeDir, "index.m3u8"))},
		key:     "live:" + id, dir: outputDir, output: &limitedBuffer{limit: 16 * 1024}, cancel: cancel,
		done: make(chan struct{}), waitResult: make(chan error, 1),
	}
	if !m.config.Pool.AcquireLive(job.key, cancel, job.done) {
		cancel()
		_ = os.RemoveAll(outputDir)
		return LiveSession{}, ErrNoTuners
	}
	command := BuildCommand(m.config.Profile, inputURL, outputDir)
	job.process = m.newProcess(command, job.output)
	if err := job.process.Start(); err != nil {
		m.config.Pool.Release(job.key)
		cancel()
		_ = os.RemoveAll(outputDir)
		return LiveSession{}, fmt.Errorf("start live TV: %w", err)
	}
	m.mu.Lock()
	m.jobs[id] = job
	m.mu.Unlock()
	m.waitGroup.Add(1)
	go m.run(jobCtx, job)

	if err := m.waitUntilReady(ctx, job); err != nil {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = m.Stop(stopCtx, id)
		stopCancel()
		return LiveSession{}, err
	}
	m.logger.Info("live TV started", "session", id, "channel", channelNumber, "pid", job.process.PID())
	return job.session, nil
}

func (m *LiveManager) Stop(ctx context.Context, id string) error {
	m.mu.Lock()
	job := m.jobs[id]
	m.mu.Unlock()
	if job == nil {
		return nil
	}
	job.cancel()
	select {
	case <-job.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *LiveManager) Wait() { m.waitGroup.Wait() }

func (m *LiveManager) run(ctx context.Context, job *liveJob) {
	defer m.waitGroup.Done()
	defer func() {
		m.mu.Lock()
		delete(m.jobs, job.session.ID)
		m.mu.Unlock()
		if err := m.removeAll(job.dir); err != nil {
			m.logger.Warn("remove live buffer", "session", job.session.ID, "error", err)
		}
	}()
	// Release the tuner before deleting the temporary HLS files. A watched live
	// buffer can contain many segments (or live on a slower mount), and cleanup
	// must never make a stopped session continue to reserve a tuner.
	defer close(job.done)
	defer m.config.Pool.Release(job.key)
	go func() { job.waitResult <- job.process.Wait() }()
	timer := time.NewTimer(m.config.MaxDuration)
	defer timer.Stop()
	select {
	case waitErr := <-job.waitResult:
		if waitErr != nil && ctx.Err() == nil {
			m.logger.Warn("live TV process exited", "session", job.session.ID, "error", waitErr, "details", strings.TrimSpace(job.output.String()))
		}
	case <-ctx.Done():
		stopProcess(job.process, job.waitResult)
	case <-timer.C:
		stopProcess(job.process, job.waitResult)
	}
}

func (m *LiveManager) waitUntilReady(ctx context.Context, job *liveJob) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(m.config.ReadyTimeout)
	defer timer.Stop()
	playlist := filepath.Join(job.dir, "index.m3u8")
	for {
		data, err := os.ReadFile(playlist)
		if err == nil && bytes.Contains(data, []byte("#EXTINF:")) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-job.done:
			details := strings.TrimSpace(job.output.String())
			if details == "" {
				details = "capture process exited before producing video"
			}
			return fmt.Errorf("start live TV: %s", details)
		case <-timer.C:
			return errors.New("live TV did not become ready in time")
		case <-ticker.C:
		}
	}
}

func stopProcess(proc process, waitResult <-chan error) {
	_ = proc.Signal(os.Interrupt)
	select {
	case <-waitResult:
		return
	case <-time.After(7 * time.Second):
		_ = proc.Kill()
		select {
		case <-waitResult:
		case <-time.After(time.Second):
		}
	}
}
