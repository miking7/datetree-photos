package main

import (
	"context"
	"errors"
	"sync"
	"time"
)

type RunningScan struct {
	Reporter  *Reporter
	Cancel    context.CancelFunc
	Source    string
	Dest      string
	Template  string
	SoftMatch bool
}

type RunningExecute struct {
	Reporter *Reporter
	Cancel   context.CancelFunc
	Source   string
	Dest     string
	Mode     string
	Moves    []PlannedMove
	Started  time.Time
}

type RunningUpdate struct {
	Reporter  *Reporter
	Cancel    context.CancelFunc
	TargetTag string
	Started   time.Time
}

// UpdateOutcome is the terminal state of an apply attempt, captured so the SSE
// handler can render the post-update screen (or a clean error) as the final
// `complete` event after the reporter closes.
type UpdateOutcome struct {
	TargetTag string
	Err       error
	Finished  time.Time
}

// DoneSnapshot is captured at the end of an execute run so the SSE handler can
// render the Done screen as the final `complete` event.
type DoneSnapshot struct {
	Source       string
	Dest         string
	Mode         string
	Completed    []CompletedMove
	ManifestPath string
	ManifestURL  string
	Started      time.Time
	Finished     time.Time
}

type Session struct {
	mu         sync.Mutex
	moves      []PlannedMove
	source     string
	dest       string
	mode       string
	running    *RunningScan
	executing  *RunningExecute
	updating   *RunningUpdate
	lastDone   *DoneSnapshot
	lastUpdate *UpdateOutcome
}

var current = &Session{}

var (
	errScanInProgress    = errors.New("a scan is already running")
	errExecuteInProgress = errors.New("an execute is already running")
	errOpInProgress      = errors.New("another operation is already running")
)

func (s *Session) Set(moves []PlannedMove, source, dest, mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.moves = moves
	s.source = source
	s.dest = dest
	s.mode = mode
}

func (s *Session) Snapshot() (moves []PlannedMove, source, dest, mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.moves, s.source, s.dest, s.mode
}

// StartScan installs the running scan record. Single-user invariant: only one
// active operation at a time, scan or execute. Caller kicks off the goroutine.
func (s *Session) StartScan(rs *RunningScan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running != nil {
		return errScanInProgress
	}
	if s.executing != nil || s.updating != nil {
		return errOpInProgress
	}
	s.running = rs
	return nil
}

func (s *Session) RunningScan() *RunningScan {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// FinishScan clears the running record and persists the resulting plan.
// Safe to call after either successful completion or cancellation. Mode is
// chosen at execute time, not scan time, so s.mode is not set here.
func (s *Session) FinishScan(moves []PlannedMove) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running != nil {
		s.moves = moves
		s.source = s.running.Source
		s.dest = s.running.Dest
		s.running = nil
	}
}

func (s *Session) StartExecute(re *RunningExecute) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running != nil || s.executing != nil || s.updating != nil {
		return errOpInProgress
	}
	s.executing = re
	return nil
}

func (s *Session) RunningExecute() *RunningExecute {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.executing
}

// FinishExecute records the Done snapshot for the SSE complete event and
// releases the running-execute slot. Clearing s.moves prevents the user from
// hitting Execute a second time on the same plan after files have been moved.
func (s *Session) FinishExecute(completed []CompletedMove, manifestPath, manifestURL string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.executing == nil {
		return
	}
	re := s.executing
	s.lastDone = &DoneSnapshot{
		Source:       re.Source,
		Dest:         re.Dest,
		Mode:         re.Mode,
		Completed:    completed,
		ManifestPath: manifestPath,
		ManifestURL:  manifestURL,
		Started:      re.Started,
		Finished:     time.Now(),
	}
	s.moves = nil
	s.executing = nil
}

func (s *Session) LastDone() *DoneSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastDone
}

func (s *Session) StartUpdate(ru *RunningUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running != nil || s.executing != nil || s.updating != nil {
		return errOpInProgress
	}
	s.updating = ru
	return nil
}

func (s *Session) RunningUpdate() *RunningUpdate {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updating
}

// FinishUpdate captures the terminal outcome and releases the running-update
// slot. The reporter Close (in the caller's defer) wakes any SSE subscriber so
// it can render the post-update screen from this snapshot.
func (s *Session) FinishUpdate(target string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.updating == nil {
		return
	}
	s.lastUpdate = &UpdateOutcome{
		TargetTag: target,
		Err:       err,
		Finished:  time.Now(),
	}
	s.updating = nil
}

func (s *Session) LastUpdate() *UpdateOutcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastUpdate
}
