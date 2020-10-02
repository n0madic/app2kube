package cmd

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"

	"github.com/n0madic/app2kube/pkg/app2kube"
	"github.com/spf13/cobra"
)

var encryptString string

// NewCmdEncrypt return encrypt command
func NewCmdEncrypt() *cobra.Command {
	encryptCmd := &cobra.Command{
		Deprecated: "Use app2kube config encrypt",
		Use:        "encrypt",
		Short:      "Encrypt secret values in YAML file",
		Long:       "Encrypts values in secrets section for specified YAML files. The result is written to the same file.",
		RunE:       encrypt,
	}
	encryptCmd.Flags().StringVarP(&encryptString, "string", "", "", "Encrypt the specified string")
	encryptCmd.Flags().VarP(&valueFiles, "values", "f", "Encrypt secrets in a file (can specify multiple)")
	return encryptCmd
}

func encrypt(cmd *cobra.Command, args []string) error {
	password, err := app2kube.GetPassword()
	if err != nil {
		return err
	}

	if encryptString != "" {
		encrypted, err := app2kube.EncryptAES(password, encryptString)
		if err != nil {
			return err
		}
		fmt.Println(app2kube.CryptPrefix + encrypted)
	} else if len(valueFiles) == 0 {
		return fmt.Errorf("need to specify yaml files")
	}

	for _, filePath := range valueFiles {
		var modified bool
		var newYAML string

		yamlFile, err := os.Open(filePath)
		if err != nil {
			return fmt.Errorf("File open error: %v", err)
		}
		defer yamlFile.Close()

		scanner := bufio.NewScanner(yamlFile)
		for scanner.Scan() {
			// found secrets section
			if strings.TrimSpace(scanner.Text()) == "secrets:" {
				newYAML += scanner.Text() + "\n"
				// scan secrets
				for scanner.Scan() {
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
							if !strings.HasPrefix(value, app2kube.CryptPrefix) {
								encrypted, err := app2kube.EncryptAES(password, value)
								if err != nil {
									return err
								}
								value = app2kube.CryptPrefix + encrypted
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
			err = ioutil.WriteFile(filePath, []byte(newYAML), 0640)
			if err != nil {
				return fmt.Errorf("File write error: %v", err)
			}
		}
	}

	return nil
}
