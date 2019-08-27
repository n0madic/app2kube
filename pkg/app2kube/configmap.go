package app2kube

import (
	apiv1 "k8s.io/api/core/v1"
)

// GetConfigMap resource
func (app *App) GetConfigMap() (configmap *apiv1.ConfigMap, err error) {
	if len(app.ConfigMap) > 0 {
		configmap = &apiv1.ConfigMap{
			ObjectMeta: app.GetObjectMeta(app.GetReleaseName()),
			Data:       app.ConfigMap,
		}
	}
	return configmap, nil
}
