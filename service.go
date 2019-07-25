package app2kube

import (
	"strings"

	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// GetServices YAML
func (app *App) GetServices() (yaml string) {
	if len(app.Deployment.Containers) > 0 {
		for _, svc := range app.Deployment.Service {
			if svc.Port > 0 {
				if svc.InternalPort == 0 {
					svc.InternalPort = svc.Port
				}
				if svc.ExternalPort == 0 {
					svc.ExternalPort = svc.Port
				}
			}
			serviceName := app.Name + "-" + strings.ToLower(svc.Name)
			service := &apiv1.Service{
				ObjectMeta: app.GetObjectMeta(serviceName),
				Spec: apiv1.ServiceSpec{
					Ports: []apiv1.ServicePort{apiv1.ServicePort{
						Port:       svc.ExternalPort,
						Protocol:   svc.Protocol,
						TargetPort: intstr.IntOrString{Type: intstr.Int, IntVal: svc.InternalPort},
					}},
					Type:     svc.Type,
					Selector: app.Labels,
				},
			}
			if svc.Type == apiv1.ServiceTypeNodePort {
				service.Spec.Ports[0].NodePort = svc.ExternalPort
			}
			yaml = yaml + getYAML("Service: "+serviceName, service)
		}
	}
	return
}
