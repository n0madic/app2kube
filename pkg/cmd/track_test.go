package cmd

import (
	"testing"
	"time"
)

// Regression (#32): the "logs since" start time must be computed at command
// execution time (passed an explicit now), not fixed at binary-init time.
func TestResolveLogsFrom(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	if got, err := resolveLogsFrom("now", now); err != nil || !got.Equal(now) {
		t.Errorf(`"now": got %v (err %v), want %v`, got, err, now)
	}
	if got, err := resolveLogsFrom("all", now); err != nil || !got.IsZero() {
		t.Errorf(`"all": expected zero time, got %v (err %v)`, got, err)
	}
	if got, err := resolveLogsFrom("30m", now); err != nil || !got.Equal(now.Add(-30*time.Minute)) {
		t.Errorf(`"30m": got %v (err %v), want %v`, got, err, now.Add(-30*time.Minute))
	}
	// An unparseable value is a user error and must be rejected, not silently
	// treated as "now" (which would hide the requested past logs).
	if _, err := resolveLogsFrom("garbage", now); err == nil {
		t.Errorf("invalid duration must return an error, got nil")
	}
}
