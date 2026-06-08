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

// resolvePorts resolves the (internalPort, externalPort) pair a Service exposes:
// Port fills in either when unset, then each side falls back to the other. ok is
// false when neither port is set. effectiveServicePort and GetServices both use
// it so the precedence lives in one place.
func (svc Service) resolvePorts() (internal, external int32, ok bool) {
	internal, external = svc.InternalPort, svc.ExternalPort
	if svc.Port > 0 {
		if internal == 0 {
			internal = svc.Port
		}
		if external == 0 {
			external = svc.Port
		}
	}
	if internal == 0 && external == 0 {
		return 0, 0, false
	}
	if external == 0 {
		external = internal
	}
	if internal == 0 {
		internal = external
	}
	return internal, external, true
}

// effectiveServicePort returns the external port the generated Service exposes
// (ExternalPort, else Port, else InternalPort). GetIngress uses this so a Service
// defined with only internalPort still wires up a valid ingress backend instead
// of failing with "you must specify a servicePort" (#16).
func (svc Service) effectiveServicePort() int32 {
	_, external, _ := svc.resolvePorts()
	return external
}

// GetServices resource
func (app *App) GetServices() (services []*apiv1.Service, err error) {
	if len(app.Deployment.Containers) > 0 {
		// Iterate in sorted key order so the rendered Service list (and the
		// ---separated documents it produces) is stable across runs; a map-random
		// order would reorder the manifest on every render and show up as a
		// spurious change in kubectl apply/diff for any app exposing >1 service.
		for _, name := range sortedKeys(app.Service) {
			svc := app.Service[name]
			internal, external, ok := svc.resolvePorts()
			if !ok {
				return services, fmt.Errorf("port required for service: %s", name)
			}
			svc.InternalPort = internal
			svc.ExternalPort = external

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
