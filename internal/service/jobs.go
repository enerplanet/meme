// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/enerplanet/meme/internal/model"
)

// JobState is the lifecycle of an async simulation job.
type JobState string

const (
	StateQueued    JobState = "queued"
	StateRunning   JobState = "running"
	StateSucceeded JobState = "succeeded"
	StateFailed    JobState = "failed"
)

// Done reports whether the state is terminal.
func (s JobState) Done() bool { return s == StateSucceeded || s == StateFailed }

// JobRecord is a single submitted simulation. All mutation goes through its
// mutex so a background worker and HTTP readers can share it safely. The
// console log is written through to log.txt in the job dir (so large solver
// logs don't accumulate in memory and the bundle picks the file up directly).
type JobRecord struct {
	mu       sync.Mutex
	id       string
	target   model.Target
	state    JobState
	warnings []string
	results  []RunResult
	errMsg   string
	logPath  string
	dir      string
	created  time.Time
	started  time.Time
	finished time.Time
}

// ID returns the job identifier.
func (j *JobRecord) ID() string { return j.id }

// Dir returns the job's work directory.
func (j *JobRecord) Dir() string { return j.dir }

// AppendLog appends one line to the job's on-disk log.
func (j *JobRecord) AppendLog(line string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	f, err := os.OpenFile(j.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	_, _ = f.WriteString(line)
}

// Start marks the job running.
func (j *JobRecord) Start() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.state = StateRunning
	j.started = time.Now()
}

// Finish stores the results and derives the terminal state.
func (j *JobRecord) Finish(results []RunResult, warnings []string, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.results = results
	if len(warnings) > 0 {
		j.warnings = warnings
	}
	j.finished = time.Now()
	switch {
	case err != nil:
		j.state = StateFailed
		j.errMsg = err.Error()
	case anyRunFailed(results):
		j.state = StateFailed
		j.errMsg = "one or more runs exited non-zero"
	default:
		j.state = StateSucceeded
	}
}

func anyRunFailed(rs []RunResult) bool {
	for _, r := range rs {
		if r.ExitCode != 0 || r.Error != "" {
			return true
		}
	}
	return false
}

// JobView is the JSON-safe snapshot returned by the status endpoint.
type JobView struct {
	ID       string      `json:"id"`
	Target   string      `json:"target"`
	State    JobState    `json:"state"`
	Warnings []string    `json:"warnings,omitempty"`
	Error    string      `json:"error,omitempty"`
	Runs     []RunResult `json:"runs,omitempty"`
	Log      string      `json:"log,omitempty"`
	Created  *time.Time  `json:"created,omitempty"`
	Started  *time.Time  `json:"started,omitempty"`
	Finished *time.Time  `json:"finished,omitempty"`
}

// View returns a consistent snapshot. includeLog controls whether the (possibly
// large) console log is embedded.
func (j *JobRecord) View(includeLog bool) JobView {
	j.mu.Lock()
	defer j.mu.Unlock()
	v := JobView{
		ID: j.id, Target: string(j.target), State: j.state,
		Warnings: append([]string(nil), j.warnings...),
		Error:    j.errMsg,
		Runs:     append([]RunResult(nil), j.results...),
		Created:  nonZero(j.created), Started: nonZero(j.started), Finished: nonZero(j.finished),
	}
	if includeLog {
		v.Log = j.logText()
	}
	return v
}

// LogText returns the full console log (read from disk).
func (j *JobRecord) LogText() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.logText()
}

func (j *JobRecord) logText() string {
	b, err := os.ReadFile(j.logPath)
	if err != nil {
		return ""
	}
	return string(b)
}

func (j *JobRecord) doneBefore(cutoff time.Time) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state.Done() && !j.finished.IsZero() && j.finished.Before(cutoff)
}

func nonZero(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// JobStore is an in-memory registry of jobs keyed by id.
type JobStore struct {
	mu   sync.RWMutex
	jobs map[string]*JobRecord
}

// NewJobStore returns an empty store.
func NewJobStore() *JobStore { return &JobStore{jobs: map[string]*JobRecord{}} }

// Create registers a new queued job rooted at dir.
func (s *JobStore) Create(target model.Target, dir string, warnings []string) *JobRecord {
	rec := &JobRecord{
		id: newJobID(), target: target, state: StateQueued,
		warnings: warnings, dir: dir, created: time.Now(),
		logPath: filepath.Join(dir, "log.txt"),
	}
	s.mu.Lock()
	s.jobs[rec.id] = rec
	s.mu.Unlock()
	return rec
}

// Get looks a job up by id.
func (s *JobStore) Get(id string) (*JobRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.jobs[id]
	return rec, ok
}

// StartGC launches a background sweeper that, every interval, drops finished
// jobs older than ttl — record and work directory both — so the in-memory
// store and the work dir don't grow forever. Stops when ctx is cancelled.
func (s *JobStore) StartGC(ctx context.Context, ttl, interval time.Duration) {
	if ttl <= 0 || interval <= 0 {
		return
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.sweep(time.Now().Add(-ttl))
			}
		}
	}()
}

// sweep removes finished jobs whose completion predates cutoff.
func (s *JobStore) sweep(cutoff time.Time) {
	s.mu.Lock()
	var victims []*JobRecord
	for id, rec := range s.jobs {
		if rec.doneBefore(cutoff) {
			victims = append(victims, rec)
			delete(s.jobs, id)
		}
	}
	s.mu.Unlock()
	for _, rec := range victims {
		if rec.Dir() != "" {
			_ = os.RemoveAll(rec.Dir())
		}
	}
}

func newJobID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
