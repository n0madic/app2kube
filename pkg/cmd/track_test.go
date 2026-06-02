package cmd

import (
	"testing"
	"time"
)

// Regression (#32): the "logs since" start time must be computed at command
// execution time (passed an explicit now), not fixed at binary-init time.
func TestResolveLogsFrom(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	if got := resolveLogsFrom("now", now); !got.Equal(now) {
		t.Errorf(`"now": got %v, want %v`, got, now)
	}
	if got := resolveLogsFrom("all", now); !got.IsZero() {
		t.Errorf(`"all": expected zero time, got %v`, got)
	}
	if got := resolveLogsFrom("30m", now); !got.Equal(now.Add(-30 * time.Minute)) {
		t.Errorf(`"30m": got %v, want %v`, got, now.Add(-30*time.Minute))
	}
	if got := resolveLogsFrom("garbage", now); !got.Equal(now) {
		t.Errorf(`invalid duration must fall back to now: got %v`, got)
	}
}
