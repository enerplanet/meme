// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
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
	// LogOffset is the byte offset just past the returned log text: pass it as
	// ?since= on the next poll to fetch only what was appended.
	LogOffset *int64     `json:"log_offset,omitempty"`
	Created   *time.Time `json:"created,omitempty"`
	Started   *time.Time `json:"started,omitempty"`
	Finished  *time.Time `json:"finished,omitempty"`
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
		v.LogOffset = j.logSize()
	}
	return v
}

// LogWindow selects part of the console log for a status poll, so pollers
// need not download the whole log every time.
type LogWindow struct {
	Since    int64 // byte offset to read from (with HasSince)
	HasSince bool
	Tail     int // last Tail lines (when > 0)
}

// ViewWindow is View(true) with the log limited to w: the bytes from w.Since
// on (at most the same cap as the full view, so a poller pages forward via
// LogOffset), or the last w.Tail lines. Runs' stdout/stderr (copies of what
// the log holds) are left out. An empty window is View(true).
func (j *JobRecord) ViewWindow(w LogWindow) JobView {
	if !w.HasSince && w.Tail <= 0 {
		return j.View(true)
	}
	v := j.View(false)
	for i := range v.Runs {
		// the run output is already in the log; a windowed poll stays small
		v.Runs[i].Stdout, v.Runs[i].Stderr = "", ""
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if w.HasSince {
		v.Log, v.LogOffset = j.logSince(w.Since)
	} else {
		v.Log, v.LogOffset = j.logTail(w.Tail)
	}
	return v
}

func (j *JobRecord) logSize() *int64 {
	fi, err := os.Stat(j.logPath)
	if err != nil {
		return nil
	}
	n := fi.Size()
	return &n
}

func (j *JobRecord) logSince(off int64) (string, *int64) {
	f, err := os.Open(j.logPath)
	if err != nil {
		return "", nil
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", nil
	}
	size := fi.Size()
	if off < 0 {
		off = 0
	}
	if off >= size {
		return "", &size
	}
	n := min(size-off, int64(captureHead+captureTail))
	b := make([]byte, n)
	if _, err := f.ReadAt(b, off); err != nil && err != io.EOF {
		return "", nil
	}
	end := off + n
	return string(b), &end
}

func (j *JobRecord) logTail(lines int) (string, *int64) {
	f, err := os.Open(j.logPath)
	if err != nil {
		return "", nil
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", nil
	}
	size := fi.Size()
	n := min(size, int64(captureTail))
	b := make([]byte, n)
	if _, err := f.ReadAt(b, size-n); err != nil && err != io.EOF {
		return "", nil
	}
	s := string(b)
	// Walk back over `lines` newlines, ignoring the one that ends the log.
	cut := len(strings.TrimSuffix(s, "\n"))
	for i := 0; i < lines && cut > 0; i++ {
		cut = strings.LastIndexByte(s[:cut], '\n')
	}
	if cut > 0 {
		s = s[cut+1:]
	} else if cut == 0 {
		s = s[1:]
	}
	return s, &size
}

// LogText returns the console log (read from disk), capped to head + tail for
// huge logs — the full file stays on disk (and in the bundle's files/ tree).
func (j *JobRecord) LogText() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.logText()
}

// logText reads the log with the same head/tail cap as run output capture: the
// log view is embedded in every status poll, so it must not balloon with the
// solver's console output. The tail carries the completion/objective lines
// consumers grep for.
func (j *JobRecord) logText() string {
	f, err := os.Open(j.logPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return ""
	}
	size := fi.Size()
	if size <= int64(captureHead+captureTail) {
		b, err := io.ReadAll(f)
		if err != nil {
			return ""
		}
		return string(b)
	}
	head := make([]byte, captureHead)
	if _, err := io.ReadFull(f, head); err != nil {
		return ""
	}
	tail := make([]byte, captureTail)
	if _, err := f.ReadAt(tail, size-int64(captureTail)); err != nil {
		return ""
	}
	return string(head) + truncationMarker(size-int64(len(head)+len(tail))) + string(tail)
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
