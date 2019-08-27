package app2kube

import (
	apiv1 "k8s.io/api/core/v1"
)

// NamespaceDefault means the object is in the default namespace which is applied when not specified by clients
const NamespaceDefault = apiv1.NamespaceDefault

// GetNamespace resource
func (app *App) GetNamespace() (namespace *apiv1.Namespace) {
	if app.Namespace != "" {
		namespace = &apiv1.Namespace{}
		namespace.SetName(app.Namespace)
		if managed, ok := app.Labels["app.kubernetes.io/managed-by"]; ok {
			namespace.Labels = map[string]string{
				"app.kubernetes.io/managed-by": managed,
			}
		}
	}
	return
}
