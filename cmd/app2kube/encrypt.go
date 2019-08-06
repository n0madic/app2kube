package main

import (
	"fmt"
	"io/ioutil"
	"strings"

	"github.com/n0madic/app2kube"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
)

const dictKey = "secrets"

var encryptCmd = &cobra.Command{
	Use:   "encrypt",
	Short: "Ecrypt secret values in YAML file",
	RunE:  encrypt,
}

func init() {
	rootCmd.AddCommand(encryptCmd)
}

func encrypt(cmd *cobra.Command, args []string) error {
	password, err := app2kube.GetPassword()
	if err != nil {
		return err
	}

	for _, filePath := range valueFiles {
		m := make(map[interface{}]interface{})
		modified := false

		yamlFile, err := ioutil.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("File read error: %v", err)
		}

		err = yaml.Unmarshal(yamlFile, &m)
		if err != nil {
			return fmt.Errorf("Unmarshal %v", err)
		}

		for k, v := range m[dictKey].(map[interface{}]interface{}) {
			value := v.(string)
			if !strings.HasPrefix(value, app2kube.CryptPrefix) {
				encrypted, err := app2kube.EncryptAES(password, value)
				if err != nil {
					return err
				}
				m[dictKey].(map[interface{}]interface{})[k] = app2kube.CryptPrefix + encrypted
				modified = true
			}
		}

		if modified {
			y, err := yaml.Marshal(m)
			if err != nil {
				return err
			}

			err = ioutil.WriteFile(filePath, y, 0640)
			if err != nil {
				return fmt.Errorf("File read error: %v", err)
			}
		}
	}

	return nil
}
