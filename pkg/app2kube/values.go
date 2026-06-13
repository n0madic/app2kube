package app2kube

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig"
	"sigs.k8s.io/yaml"

	"github.com/n0madic/app2kube/internal/strvals"
)

//////////////////////////////////////
// Copied from Helm, modified by me //
//////////////////////////////////////

// ValueFiles list
type ValueFiles []string

func (v *ValueFiles) String() string {
	return fmt.Sprint(*v)
}

// Type ValueFiles
func (v *ValueFiles) Type() string {
	return "valueFiles"
}

// Set ValueFiles
func (v *ValueFiles) Set(value string) error {
	for filePath := range strings.SplitSeq(value, ",") {
		*v = append(*v, filePath)
	}
	return nil
}

// Merges source and destination map, preferring values from the source map
func mergeValues(dest map[string]any, src map[string]any) map[string]any {
	for k, v := range src {
		// If the key doesn't exist already, then just set the key to that value
		if _, exists := dest[k]; !exists {
			dest[k] = v
			continue
		}
		// A nil source value (a bare/null key like `common:` in a later -f file)
		// must not clobber an already-populated destination subtree — that would
		// silently drop the earlier file's whole map with no error. Treat it as
		// "no override" and keep the existing value.
		if v == nil {
			continue
		}
		nextMap, ok := v.(map[string]any)
		// If it isn't another map, overwrite the value
		if !ok {
			dest[k] = v
			continue
		}
		// Edge case: If the key exists in the destination, but isn't a map
		destMap, isMap := dest[k].(map[string]any)
		// If the source map has a map for this key, prefer it
		if !isMap {
			dest[k] = v
			continue
		}
		// If we got to this point, it is a map in both, so merge them
		dest[k] = mergeValues(destMap, nextMap)
	}
	return dest
}

// vals merges values from files specified via -f/--values and
// directly via --set or --set-string or --set-file, marshaling them to YAML
func vals(valueFiles ValueFiles, values, stringValues, fileValues []string) ([]byte, error) {
	base := map[string]any{}

	// User specified a values files via -f/--values
	for _, filePath := range valueFiles {
		currentMap := map[string]any{}

		var bytes []byte
		var err error
		if strings.TrimSpace(filePath) == "-" {
			bytes, err = io.ReadAll(os.Stdin)
		} else {
			bytes, err = readFile(filePath)
		}

		if err != nil {
			return []byte{}, err
		}

		// Apply template to file
		bytes, err = templating(bytes)
		if err != nil {
			return []byte{}, err
		}

		if err := yaml.Unmarshal(bytes, &currentMap); err != nil {
			return []byte{}, fmt.Errorf("failed to parse %s: %w", filePath, err)
		}
		// Merge with the previous map
		base = mergeValues(base, currentMap)
	}

	// User specified a value via --set
	for _, value := range values {
		if err := strvals.ParseInto(value, base); err != nil {
			return []byte{}, fmt.Errorf("failed parsing --set data: %w", err)
		}
	}

	// User specified a value via --set-string
	for _, value := range stringValues {
		if err := strvals.ParseIntoString(value, base); err != nil {
			return []byte{}, fmt.Errorf("failed parsing --set-string data: %w", err)
		}
	}

	// User specified a value via --set-file
	for _, value := range fileValues {
		reader := func(rs []rune) (any, error) {
			b, err := os.ReadFile(string(rs))
			if err != nil {
				// Don't derive a value on the read-error path (#39): strvals stores
				// whatever the reader returns under the key *before* it sees the
				// error, so returning a value here would leave an empty key in the
				// (then discarded) map. The error aborts the load regardless.
				return "", err
			}
			return strings.TrimSpace(string(b)), nil
		}
		if err := strvals.ParseIntoFile(value, base, reader); err != nil {
			return []byte{}, fmt.Errorf("failed parsing --set-file data: %w", err)
		}
	}

	return yaml.Marshal(base)
}

// readFile loads a file from the local filesystem. A trailing '?' marks the
// file as optional: if it is missing, a warning is printed and empty content is
// returned instead of an error.
func readFile(filePath string) ([]byte, error) {
	var allowMissing bool
	if strings.HasSuffix(filePath, "?") {
		allowMissing = true
		filePath = strings.TrimSuffix(filePath, "?")
	}

	bytes, err := os.ReadFile(filePath)
	if err != nil {
		if allowMissing {
			fmt.Fprintf(os.Stderr, "WARNING: value file missing: %s\n", err)
			return []byte{}, nil
		}
		return []byte{}, err
	}

	return bytes, nil
}

// templating renders a value file through text/template with the full sprig
// FuncMap before YAML parsing.
//
// Trust boundary: this is an intentional feature (VALUES.md documents
// {{ env "VAR" }}), so value files are treated as TRUSTED input — same trust
// level as the argv that names them. The full sprig map includes env/expandenv
// (host environment access) and getHostByName (DNS lookup), so a value file from
// an untrusted source could exfiltrate host secrets or probe via DNS. Do not feed
// attacker-influenced value files to app2kube; if that is ever required, strip
// env/expandenv/getHostByName from the FuncMap here.
func templating(raw []byte) ([]byte, error) {
	tmpl, err := template.New("values").Funcs(sprig.FuncMap()).Parse(string(raw))
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer
	err = tmpl.Execute(&b, nil)
	if err != nil {
		return nil, err
	}

	return b.Bytes(), nil
}
