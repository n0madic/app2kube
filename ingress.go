package app2kube

import (
	"fmt"
	"strconv"
	"strings"

	apiv1 "k8s.io/api/core/v1"
	"k8s.io/api/extensions/v1beta1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// GetIngress resource
func (app *App) GetIngress(ingressClass string) (ingress []*v1beta1.Ingress, err error) {
	if len(app.Deployment.Containers) > 0 && len(app.Deployment.Service) > 0 {
		for _, ing := range app.Deployment.Ingress {
			if app.Staging != "" {
				if strings.HasPrefix(ing.Host, "*") {
					return ingress, fmt.Errorf("staging cannot be used with wildcard domain: %s", ing.Host)
				}
				ing.Host = app.Staging + "." + ing.Host
				if app.Branch != "" {
					ing.Host = app.Branch + "." + ing.Host
				}
			}

			ingressName := app.Name + "-" + strings.Replace(ing.Host, "*", "wildcard", 1)

			ingressAnnotations := make(map[string]string)
			ingressAnnotations["kubernetes.io/ingress.class"] = ingressClass
			if ing.Letsencrypt {
				ingressAnnotations["kubernetes.io/tls-acme"] = "true"
			}
			for key, value := range ing.Annotations {
				ingressAnnotations[key] = value
			}

			serviceName := ing.ServiceName
			if serviceName == "" {
				serviceName = app.GetReleaseName() + "-" + app.Deployment.Service[0].Name
			}

			servicePort := ing.ServicePort
			if servicePort == 0 {
				if app.Deployment.Service[0].ExternalPort > 0 {
					servicePort = app.Deployment.Service[0].ExternalPort
				} else {
					servicePort = app.Deployment.Service[0].Port
				}
			}

			if ing.Path == "" {
				ing.Path = "/"
			}

			ingressRuleValue := v1beta1.IngressRuleValue{
				HTTP: &v1beta1.HTTPIngressRuleValue{
					Paths: []v1beta1.HTTPIngressPath{v1beta1.HTTPIngressPath{
						Path: ing.Path,
						Backend: v1beta1.IngressBackend{
							ServiceName: strings.ToLower(serviceName),
							ServicePort: intstr.IntOrString{Type: intstr.Int, IntVal: servicePort},
						},
					}},
				},
			}
			ingressRules := []v1beta1.IngressRule{v1beta1.IngressRule{
				Host:             ing.Host,
				IngressRuleValue: ingressRuleValue,
			}}

			ingressTLS := []v1beta1.IngressTLS{}
			if ing.Letsencrypt || ing.TLSSecretName != "" || (ing.TLSCrt != "" && ing.TLSKey != "") {
				ingressAnnotations["nginx.ingress.kubernetes.io/ssl-redirect"] = strconv.FormatBool(ing.SslRedirect)
				if ing.TLSSecretName == "" {
					ing.TLSSecretName = "tls-" + strings.Replace(ing.Host, "*", "wildcard", 1)
				}
				ingressTLS = append(ingressTLS, v1beta1.IngressTLS{
					Hosts:      []string{ing.Host},
					SecretName: strings.ToLower(ing.TLSSecretName),
				})
			}

			if app.Staging == "" {
				for _, alias := range ing.Aliases {
					ingressRules = append(ingressRules, v1beta1.IngressRule{
						Host:             alias,
						IngressRuleValue: ingressRuleValue,
					})
					if ing.Letsencrypt || ing.TLSSecretName != "" || (ing.TLSCrt != "" && ing.TLSKey != "") {
						ingressTLS[0].Hosts = append(ingressTLS[0].Hosts, alias)
					}
				}
			}

			ingressMeta := app.GetObjectMeta(ingressName)
			ingressMeta.Annotations = ingressAnnotations

			ingressObj := &v1beta1.Ingress{
				ObjectMeta: ingressMeta,
				Spec: v1beta1.IngressSpec{
					Rules: ingressRules,
					TLS:   ingressTLS,
				},
			}
			ingress = append(ingress, ingressObj)
		}
	}
	return ingress, nil
}

// GetIngressSecrets return TLS secrets for ingress
func (app *App) GetIngressSecrets() (secrets []*apiv1.Secret) {
	if len(app.Deployment.Containers) > 0 && len(app.Deployment.Service) > 0 {
		for _, ingress := range app.Deployment.Ingress {
			if ingress.Letsencrypt || (ingress.TLSCrt != "" && ingress.TLSKey != "") {
				if ingress.TLSSecretName == "" {
					ingress.TLSSecretName = "tls-" + strings.Replace(ingress.Host, "*", "wildcard", 1)
				}
				secret := &apiv1.Secret{
					ObjectMeta: app.GetObjectMeta(ingress.TLSSecretName),
					Data: map[string][]byte{
						"tls.crt": []byte(ingress.TLSCrt),
						"tls.key": []byte(ingress.TLSKey),
					},
					Type: apiv1.SecretTypeTLS,
				}
				secrets = append(secrets, secret)
			}
		}
	}
	return
}
