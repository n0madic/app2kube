package app2kube

import (
	"bytes"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/Masterminds/sprig"
	"github.com/ghodss/yaml"
	"k8s.io/helm/pkg/strvals"
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
	for _, filePath := range strings.Split(value, ",") {
		*v = append(*v, filePath)
	}
	return nil
}

// Merges source and destination map, preferring values from the source map
func mergeValues(dest map[string]interface{}, src map[string]interface{}) map[string]interface{} {
	for k, v := range src {
		// If the key doesn't exist already, then just set the key to that value
		if _, exists := dest[k]; !exists {
			dest[k] = v
			continue
		}
		nextMap, ok := v.(map[string]interface{})
		// If it isn't another map, overwrite the value
		if !ok {
			dest[k] = v
			continue
		}
		// Edge case: If the key exists in the destination, but isn't a map
		destMap, isMap := dest[k].(map[string]interface{})
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
	base := map[string]interface{}{}

	// User specified a values files via -f/--values
	for _, filePath := range valueFiles {
		currentMap := map[string]interface{}{}

		var bytes []byte
		var err error
		if strings.TrimSpace(filePath) == "-" {
			bytes, err = ioutil.ReadAll(os.Stdin)
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
			return []byte{}, fmt.Errorf("failed to parse %s: %s", filePath, err)
		}
		// Merge with the previous map
		base = mergeValues(base, currentMap)
	}

	// User specified a value via --set
	for _, value := range values {
		if err := strvals.ParseInto(value, base); err != nil {
			return []byte{}, fmt.Errorf("failed parsing --set data: %s", err)
		}
	}

	// User specified a value via --set-string
	for _, value := range stringValues {
		if err := strvals.ParseIntoString(value, base); err != nil {
			return []byte{}, fmt.Errorf("failed parsing --set-string data: %s", err)
		}
	}

	// User specified a value via --set-file
	for _, value := range fileValues {
		reader := func(rs []rune) (interface{}, error) {
			bytes, err := ioutil.ReadFile(string(rs))
			return string(bytes), err
		}
		if err := strvals.ParseIntoFile(value, base, reader); err != nil {
			return []byte{}, fmt.Errorf("failed parsing --set-file data: %s", err)
		}
	}

	return yaml.Marshal(base)
}

// readFile load a file from the local directory or a remote file with a url.
func readFile(filePath string) ([]byte, error) {
	var allowMissing bool
	if strings.HasSuffix(filePath, "?") {
		allowMissing = true
		filePath = strings.TrimSuffix(filePath, "?")
	}

	u, err := url.Parse(filePath)
	if err != nil {
		return nil, err
	}

	var bytes []byte
	if strings.HasPrefix(u.Scheme, "http") {
		resp, err := http.Get(filePath)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			bytes, err = ioutil.ReadAll(resp.Body)
			if err != nil {
				return nil, err
			}
		} else {
			err = fmt.Errorf("%s loading error: %s", filePath, resp.Status)
			if allowMissing {
				fmt.Fprintf(os.Stderr, "WARNING: value URL missing: %s\n", err)
			} else {
				return nil, err
			}
		}
	} else {
		bytes, err = ioutil.ReadFile(filePath)
		if err != nil {
			if allowMissing {
				fmt.Fprintf(os.Stderr, "WARNING: value file missing: %s\n", err)
			} else {
				return []byte{}, err
			}
		}
	}

	return bytes, nil
}

// templating values
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
