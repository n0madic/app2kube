package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/n0madic/app2kube/pkg/app2kube"
	"go.yaml.in/yaml/v3"
)

// decodeYAMLScalar parses a single YAML value fragment (everything after the
// first colon on a secrets line) and returns its decoded scalar string. Unlike
// strconv.Unquote it understands YAML quoting — single quotes ('it”s'), YAML
// escape rules — and strips trailing inline comments.
//
// It returns ok=false when the fragment is not a plain scalar. The important case
// is a Go template in value position ({{ ... }}), which YAML parses as a flow
// mapping rather than a string; the caller leaves such values verbatim instead of
// mangling them, since the normal pipeline renders templates before YAML parsing.
func decodeYAMLScalar(raw string) (string, bool) {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &node); err != nil {
		return "", false
	}
	if len(node.Content) != 1 || node.Content[0].Kind != yaml.ScalarNode {
		return "", false
	}
	return node.Content[0].Value, true
}

// resolveEncryptString returns the plaintext to encrypt for `config encrypt
// --string`. A literal value is returned as-is; the sentinel "-" reads the
// secret from stdin (bounded, trailing newline trimmed) so it never lands in
// shell history or ps output via argv (#64), mirroring build's --password-stdin.
func resolveEncryptString(s string, stdin io.Reader) (string, error) {
	if s == "-" {
		return readStdinSecret(stdin)
	}
	return s, nil
}

func leadingWhitespace(s string) int {
	for i, r := range s {
		if r != ' ' && r != '\t' {
			return i
		}
	}
	return len(s)
}

// runEncrypt encrypts the given string and/or the secrets sections of the
// provided value files in place.
func runEncrypt(encryptString string, valueFiles app2kube.ValueFiles) error {
	app := app2kube.NewApp()

	if encryptString != "" {
		encrypted, err := app.EncryptSecret(encryptString)
		if err != nil {
			return err
		}
		fmt.Println(encrypted)
	} else if len(valueFiles) == 0 {
		return fmt.Errorf("need to specify yaml files")
	}

	for _, filePath := range valueFiles {
		var modified bool
		var newYAML string

		content, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("file open error: %w", err)
		}

		scanner := bufio.NewScanner(strings.NewReader(string(content)))
		var pendingLine *string
		nextLine := func() (string, bool) {
			if pendingLine != nil {
				line := *pendingLine
				pendingLine = nil
				return line, true
			}
			if !scanner.Scan() {
				return "", false
			}
			return scanner.Text(), true
		}
		unreadLine := func(line string) {
			pendingLine = &line
		}

		for {
			line, ok := nextLine()
			if !ok {
				break
			}
			// found secrets section
			if strings.TrimSpace(line) == "secrets:" {
				newYAML += line + "\n"
				// scan secrets
				for {
					line, ok := nextLine()
					if !ok {
						break
					}
					// blank lines belong to the section, keep them and continue
					if strings.TrimSpace(line) == "" {
						newYAML += line + "\n"
						continue
					}
					// skip template lines
					if strings.HasPrefix(strings.TrimSpace(line), "{{") {
						newYAML += line + "\n"
						continue
					}
					// process line with secret
					if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
						v := strings.SplitN(line, ":", 2)
						if len(v) == 2 {
							stripped := strings.TrimSpace(v[1])
							// A YAML block scalar (key: | / key: >) puts the secret on
							// the following, more-indented lines, which this line-based
							// rewriter does not understand. Warn loudly and leave it
							// verbatim instead of "encrypting" the bare |/> indicator and
							// giving a false sense the secret was protected.
							if stripped == "|" || stripped == ">" ||
								strings.HasPrefix(stripped, "|") || strings.HasPrefix(stripped, ">") {
								fmt.Fprintf(os.Stderr, "WARNING: secret %q uses a YAML block scalar and was NOT encrypted; inline the value as a plain scalar to encrypt it\n", strings.TrimSpace(v[0]))
								newYAML += line + "\n"
								keyIndent := leadingWhitespace(line)
								for {
									blockLine, ok := nextLine()
									if !ok {
										break
									}
									if strings.TrimSpace(blockLine) == "" || leadingWhitespace(blockLine) > keyIndent {
										newYAML += blockLine + "\n"
										continue
									}
									unreadLine(blockLine)
									break
								}
								continue
							}
							value, ok := decodeYAMLScalar(v[1])
							if !ok {
								// Not a plain YAML scalar. The common case is a Go
								// template in value position ({{ ... }}); the normal
								// pipeline renders templates BEFORE YAML parsing, so
								// encrypting the directive would store its literal text
								// and defeat templating. Leave it verbatim — but warn for
								// a non-empty, non-template value so a genuinely
								// unparseable secret is not silently skipped.
								if stripped != "" && !strings.HasPrefix(stripped, "{{") {
									fmt.Fprintf(os.Stderr, "WARNING: secret %q is not a plain scalar and was NOT encrypted\n", strings.TrimSpace(v[0]))
								}
								newYAML += line + "\n"
								continue
							}
							// encrypt value
							if !app2kube.IsEncrypted(value) {
								encrypted, err := app.EncryptSecret(value)
								if err != nil {
									return err
								}
								value = encrypted
								modified = true
							}
							newYAML += fmt.Sprintf("%s: %s\n", v[0], value)
						} else {
							newYAML += line + "\n"
						}
					} else {
						newYAML += line + "\n"
						break // if a new section begins
					}
				}
			} else {
				newYAML += line + "\n"
			}
		}

		if err := scanner.Err(); err != nil {
			return err
		}

		if modified {
			// Write the secrets file owner-only (0600). os.WriteFile only applies
			// the mode when it creates the file, so Chmod afterwards tightens an
			// existing file that was previously group/world-readable — a secrets
			// file should never be.
			err = os.WriteFile(filePath, []byte(newYAML), 0600)
			if err != nil {
				return fmt.Errorf("file write error: %w", err)
			}
			if err = os.Chmod(filePath, 0600); err != nil {
				return fmt.Errorf("file chmod error: %w", err)
			}
		}
	}

	return nil
}
