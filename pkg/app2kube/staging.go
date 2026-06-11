package app2kube

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Staging is the staging environment selector. It accepts either form in values:
//
//   - a string — a named environment whose name becomes a domain segment and the
//     instance label (e.g. `staging: stg` -> stg.host / stg-branch.host);
//   - the boolean true — an *anonymous* staging: the staging machinery is active
//     but nothing is prepended to the domain or labels, so a branch is published
//     onto the root domain (`staging: true` + `branch: feat` -> feat.host).
//
// The strings "true"/"false" are reserved and treated as the matching boolean,
// so `--set staging=true` (parsed as a YAML bool) and a quoted `staging: "true"`
// or `--set-string staging=true` behave identically — and no environment can be
// named "true"/"false". The zero value (false / absent) means no staging.
type Staging struct {
	// Active reports whether staging machinery is on (named OR anonymous).
	Active bool
	// Name is the staging environment name; empty for anonymous staging.
	Name string
}

// UnmarshalJSON accepts a boolean or a string. sigs.k8s.io/yaml routes YAML
// through encoding/json, so this is the single decode path for both -f files and
// --set values.
func (s *Staging) UnmarshalJSON(data []byte) error {
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		s.Active = b
		s.Name = ""
		return nil
	}

	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return fmt.Errorf("staging must be a string or boolean, got %s", data)
	}

	switch strings.ToLower(str) {
	case "true":
		s.Active, s.Name = true, ""
	case "false", "":
		s.Active, s.Name = false, ""
	default:
		s.Active, s.Name = true, str
	}
	return nil
}
