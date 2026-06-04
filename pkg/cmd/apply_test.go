package cmd

import (
	"slices"
	"strings"
	"testing"
)

// Regression: a PodDisruptionBudget is emitted with the Deployment but is
// conditional on replicas>1, so it can drop out of the manifest. `apply --prune`
// must be allowed to delete it, otherwise scaling back to a single replica
// orphans a minAvailable PDB that blocks every node drain.
func TestApplyPruneWhitelistIncludesPDB(t *testing.T) {
	if !slices.Contains(applyPruneWhitelist, "policy/v1/PodDisruptionBudget") {
		t.Errorf("apply --prune whitelist must include policy/v1/PodDisruptionBudget: %v", applyPruneWhitelist)
	}
}

// Regression: kubectl's "all" category excludes PodDisruptionBudgets, so
// `delete all` must name poddisruptionbudgets explicitly or the PDB survives
// teardown and keeps blocking voluntary disruptions.
func TestDeleteAllResourceTypesIncludesPDB(t *testing.T) {
	if !strings.Contains(deleteAllResourceTypes, "poddisruptionbudget") {
		t.Errorf("`delete all` resource list must include poddisruptionbudgets: %q", deleteAllResourceTypes)
	}
}

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
