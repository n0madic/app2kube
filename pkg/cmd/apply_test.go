package cmd

import "testing"

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
