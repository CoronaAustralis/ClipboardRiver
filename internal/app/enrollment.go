package app

import (
	"sync"
	"time"

	"github.com/clipboardriver/cb_river_server/internal/auth"
)

const enrollmentDuration = 5 * time.Minute

type enrollmentWindow struct {
	mu       sync.Mutex
	code     string
	deadline time.Time
	timer    *time.Timer
}

type enrollmentSnapshot struct {
	Open             bool
	Code             string
	ClosesAt         time.Time
	RemainingSeconds int
}

func (e *enrollmentWindow) open() error {
	code, err := auth.RandomToken(8)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closeLocked()
	e.code = code
	e.deadline = time.Now().Add(enrollmentDuration)
	// The deadline identifies the window so a previous timer cannot close a new one.
	deadline := e.deadline
	e.timer = time.AfterFunc(enrollmentDuration, func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.deadline.Equal(deadline) {
			e.closeLocked()
		}
	})
	return nil
}

func (e *enrollmentWindow) closeLocked() {
	if e.timer != nil {
		e.timer.Stop()
		e.timer = nil
	}
	e.code = ""
	e.deadline = time.Time{}
}

func (e *enrollmentWindow) close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closeLocked()
}

func (e *enrollmentWindow) authorize(code string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.code == "" {
		return false
	}
	if !time.Now().Before(e.deadline) {
		e.closeLocked()
		return false
	}
	return compareStrings(e.code, code)
}

func (e *enrollmentWindow) snapshot() enrollmentSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.code == "" {
		return enrollmentSnapshot{}
	}
	remaining := time.Until(e.deadline)
	if remaining <= 0 {
		e.closeLocked()
		return enrollmentSnapshot{}
	}
	return enrollmentSnapshot{Open: true, Code: e.code, ClosesAt: e.deadline, RemainingSeconds: int((remaining + time.Second - 1) / time.Second)}
}
