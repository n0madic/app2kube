package app2kube

import (
	apiv1 "k8s.io/api/core/v1"
)

// GetPersistentVolumeClaims YAML
func (app *App) GetPersistentVolumeClaims() (yaml string) {
	for volName, volume := range app.Volumes {
		claimName := app.GetReleaseName() + "-" + volName

		claim := &apiv1.PersistentVolumeClaim{
			ObjectMeta: app.GetObjectMeta(claimName),
			Spec:       volume.Spec,
		}

		yaml = yaml + getYAML(claim)
	}
	return
}
