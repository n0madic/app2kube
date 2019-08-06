package app2kube

import (
	"fmt"

	apiv1 "k8s.io/api/core/v1"
)

// GetPersistentVolumeClaims YAML
func (app *App) GetPersistentVolumeClaims() (claims []*apiv1.PersistentVolumeClaim, err error) {
	for volName, volume := range app.Volumes {
		if volume.MountPath == "" {
			return claims, fmt.Errorf("mount path required for PVC: %s", volName)
		}

		claimName := app.GetReleaseName() + "-" + volName

		claim := &apiv1.PersistentVolumeClaim{
			ObjectMeta: app.GetObjectMeta(claimName),
			Spec:       volume.Spec,
		}

		claims = append(claims, claim)
	}
	return
}
