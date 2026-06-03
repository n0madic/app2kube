package cmd

import (
	"strings"
	"testing"
)

// #60: on a blue/green phase-1 or trackReady failure the operator must be told
// the new color was deployed but traffic was NOT switched, and that re-running
// is safe.
func TestBlueGreenNotSwitchedMsg(t *testing.T) {
	msg := strings.ToLower(blueGreenNotSwitchedMsg("green"))
	for _, want := range []string{"green", "not switched", "re-run"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q must mention %q", msg, want)
		}
	}
}

// Regression (#26): the --track value must be validated at parse time (before
// the cluster is mutated), accepting only "", "ready", "follow" (case-insensitive)
// and rejecting typos like "redy".
func TestValidateTrackValue(t *testing.T) {
	for _, v := range []string{"", "ready", "follow", "READY", "Follow"} {
		if err := validateTrackValue(v); err != nil {
			t.Errorf("valid track %q rejected: %v", v, err)
		}
	}
	for _, v := range []string{"redy", "yes", "true", "foll", "status"} {
		if err := validateTrackValue(v); err == nil {
			t.Errorf("invalid track %q accepted", v)
		}
	}
}
