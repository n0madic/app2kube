package app2kube

import (
	"fmt"
	"os"

	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// nodePortInRange reports whether p is a valid Kubernetes node port (the default
// service-node-port-range, 30000-32767). A requested NodePort outside it cannot
// be pinned — the apiserver would reject it — so it is left unset for
// auto-assignment (#49).
func nodePortInRange(p int32) bool {
	return p >= 30000 && p <= 32767
}

// effectiveServicePort returns the port the generated Service exposes, resolved
// the same way GetServices resolves it: ExternalPort, else Port, else
// InternalPort. GetIngress uses this so a Service defined with only
// internalPort still wires up a valid ingress backend instead of failing with
// "you must specify a servicePort" (#16).
func (svc Service) effectiveServicePort() int32 {
	switch {
	case svc.ExternalPort > 0:
		return svc.ExternalPort
	case svc.Port > 0:
		return svc.Port
	default:
		return svc.InternalPort
	}
}

// GetServices resource
func (app *App) GetServices() (services []*apiv1.Service, err error) {
	if len(app.Deployment.Containers) > 0 {
		for name, svc := range app.Service {
			if svc.Port > 0 {
				if svc.InternalPort == 0 {
					svc.InternalPort = svc.Port
				}
				if svc.ExternalPort == 0 {
					svc.ExternalPort = svc.Port
				}
			}

			if svc.InternalPort == 0 && svc.ExternalPort == 0 {
				return services, fmt.Errorf("port required for service: %s", name)
			}

			if svc.InternalPort != 0 && svc.ExternalPort == 0 {
				svc.ExternalPort = svc.InternalPort
			}
			if svc.ExternalPort != 0 && svc.InternalPort == 0 {
				svc.InternalPort = svc.ExternalPort
			}

			serviceName := app.GetServiceName(name)

			service := &apiv1.Service{
				ObjectMeta: app.GetObjectMeta(serviceName),
				Spec: apiv1.ServiceSpec{
					Ports: []apiv1.ServicePort{{
						Port:       svc.ExternalPort,
						Protocol:   svc.Protocol,
						TargetPort: intstr.IntOrString{Type: intstr.Int, IntVal: svc.InternalPort},
					}},
					Type:     svc.Type,
					Selector: app.GetColorLabels(),
				},
			}

			// Only pin a node port when the requested ExternalPort falls inside
			// the default node-port range; otherwise leave it unset so the
			// apiserver auto-assigns a valid port instead of rejecting the
			// Service (e.g. ExternalPort=80 is not a valid node port). Warn rather
			// than dropping it silently (#49).
			if svc.Type == apiv1.ServiceTypeNodePort {
				if nodePortInRange(svc.ExternalPort) {
					service.Spec.Ports[0].NodePort = svc.ExternalPort
				} else if svc.ExternalPort > 0 {
					fmt.Fprintf(os.Stderr, "WARNING: service %q requests node port %d outside the valid range 30000-32767; leaving it unset for the apiserver to auto-assign\n", name, svc.ExternalPort)
				}
			}

			services = append(services, service)
		}
	}
	return
}
