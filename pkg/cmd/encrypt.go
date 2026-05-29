package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/n0madic/app2kube/pkg/app2kube"
)

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
			return fmt.Errorf("file open error: %v", err)
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
			err = os.WriteFile(filePath, []byte(newYAML), 0640)
			if err != nil {
				return fmt.Errorf("file write error: %v", err)
			}
		}
	}

	return nil
}
