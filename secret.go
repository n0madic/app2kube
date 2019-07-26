package app2kube

import (
	apiv1 "k8s.io/api/core/v1"
)

// GetSecret YAML
func (app *App) GetSecret() (yaml string) {
	if len(app.Secrets) > 0 {
		secretBytes := make(map[string][]byte)
		for key, value := range app.Secrets {
			secretBytes[key] = []byte(value)
		}

		secret := &apiv1.Secret{
			ObjectMeta: app.GetObjectMeta(app.GetReleaseName()),
			Data:       secretBytes,
		}

		yaml = getYAML("Secret: "+app.GetReleaseName(), secret)
	}
	return
}
