package app2kube

import (
	"fmt"

	apiv1 "k8s.io/api/core/v1"
)

// GetPersistentVolumeClaims resource
func (app *App) GetPersistentVolumeClaims() (claims []*apiv1.PersistentVolumeClaim, err error) {
	for _, volName := range sortedKeys(app.Volumes) {
		volume := app.Volumes[volName]
		if volume.MountPath == "" {
			return claims, fmt.Errorf("mount path required for PVC: %s", volName)
		}
		// An omitted accessModes yields a PVC the apiserver rejects; fail fast
		// here with a clear, attributable error instead (#48).
		if len(volume.Spec.AccessModes) == 0 {
			return claims, fmt.Errorf("accessModes required for PVC: %s", volName)
		}

		claimName := app.GetVolumeClaimName(volName)

		claim := &apiv1.PersistentVolumeClaim{
			ObjectMeta: app.GetObjectMeta(claimName),
			Spec:       volume.Spec,
		}

		claims = append(claims, claim)
	}
	return
}

// pvcAllowsMultiAttach reports whether the access modes let more than one pod
// (potentially on different nodes) mount the volume — i.e. ReadWriteMany or
// ReadOnlyMany. A ReadWriteOnce(-Pod) volume cannot, so mounting it into a
// multi-replica Deployment deadlocks scheduling (#48).
func pvcAllowsMultiAttach(modes []apiv1.PersistentVolumeAccessMode) bool {
	for _, m := range modes {
		if m == apiv1.ReadWriteMany || m == apiv1.ReadOnlyMany {
			return true
		}
	}
	return false
}
