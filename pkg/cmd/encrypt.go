package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/n0madic/app2kube/pkg/app2kube"
)

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
		for scanner.Scan() {
			// found secrets section
			if strings.TrimSpace(scanner.Text()) == "secrets:" {
				newYAML += scanner.Text() + "\n"
				// scan secrets
				for scanner.Scan() {
					// blank lines belong to the section, keep them and continue
					if strings.TrimSpace(scanner.Text()) == "" {
						newYAML += scanner.Text() + "\n"
						continue
					}
					// skip template lines
					if strings.HasPrefix(strings.TrimSpace(scanner.Text()), "{{") {
						newYAML += scanner.Text() + "\n"
						continue
					}
					// process line with secret
					if strings.HasPrefix(scanner.Text(), " ") || strings.HasPrefix(scanner.Text(), "\t") {
						v := strings.SplitN(scanner.Text(), ":", 2)
						if len(v) == 2 {
							// unquote value if necessary
							stripped := strings.TrimSpace(v[1])
							// A YAML block scalar (key: | / key: >) puts the secret on
							// the following, more-indented lines, which this line-based
							// rewriter does not understand. Warn loudly and leave it
							// verbatim instead of "encrypting" the bare |/> indicator and
							// giving a false sense the secret was protected.
							if stripped == "|" || stripped == ">" ||
								strings.HasPrefix(stripped, "|") || strings.HasPrefix(stripped, ">") {
								fmt.Fprintf(os.Stderr, "WARNING: secret %q uses a YAML block scalar and was NOT encrypted; inline the value as a plain scalar to encrypt it\n", strings.TrimSpace(v[0]))
								newYAML += scanner.Text() + "\n"
								continue
							}
							value, err := strconv.Unquote(stripped)
							if err != nil {
								value = stripped
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
							newYAML += scanner.Text() + "\n"
						}
					} else {
						newYAML += scanner.Text() + "\n"
						break // if a new section begins
					}
				}
			} else {
				newYAML += scanner.Text() + "\n"
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
