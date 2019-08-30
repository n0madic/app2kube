package app2kube

import (
	"bytes"
	"fmt"
	"io"
	"reflect"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/kubectl/pkg/scheme"
)

// OutputResource type
type OutputResource int

const (
	// OutputAll for all resources (except namespace)
	OutputAll OutputResource = iota
	// OutputAllForDeployment is all the resources needed to run Deployment
	OutputAllForDeployment
	// OutputAllOther is all other resources not needed to run Deployment
	OutputAllOther
	// OutputConfigMap only
	OutputConfigMap
	// OutputCronJob only
	OutputCronJob
	// OutputDeployment only
	OutputDeployment
	// OutputIngress only
	OutputIngress
	// OutputNamespace only
	OutputNamespace
	// OutputPersistentVolumeClaim only
	OutputPersistentVolumeClaim
	// OutputSecret only
	OutputSecret
	// OutputService only
	OutputService
)

// GetManifest returns a manifest with the specified resource types
func (app *App) GetManifest(outputFormat string, typeOutput ...OutputResource) (manifest string, err error) {
	for _, out := range typeOutput {
		if out == OutputNamespace {
			namespace := app.GetNamespace()
			yml, err := PrintObj(namespace, outputFormat)
			if err != nil {
				return "", err
			}
			manifest += yml
		}

		if out == OutputAll || out == OutputAllForDeployment || out == OutputSecret {
			secret, err := app.GetSecret()
			if err != nil {
				return "", err
			}
			yml, err := PrintObj(secret, outputFormat)
			if err != nil {
				return "", err
			}
			manifest += yml
		}

		if out == OutputAll || out == OutputAllForDeployment || out == OutputConfigMap {
			configmap, err := app.GetConfigMap()
			if err != nil {
				return "", err
			}
			yml, err := PrintObj(configmap, outputFormat)
			if err != nil {
				return "", err
			}
			manifest += yml
		}

		if out == OutputAll || out == OutputAllForDeployment || out == OutputPersistentVolumeClaim {
			claims, err := app.GetPersistentVolumeClaims()
			if err != nil {
				return "", err
			}
			for _, claim := range claims {
				yml, err := PrintObj(claim, outputFormat)
				if err != nil {
					return "", err
				}
				manifest += yml
			}
		}

		if out == OutputAll || out == OutputAllOther || out == OutputCronJob {
			jobs, err := app.GetCronJobs()
			if err != nil {
				return "", err
			}
			for _, cron := range jobs {
				yml, err := PrintObj(cron, outputFormat)
				if err != nil {
					return "", err
				}
				manifest += yml
			}
		}

		if out == OutputAll || out == OutputAllForDeployment || out == OutputDeployment {
			deployment, err := app.GetDeployment()
			if err != nil {
				return "", err
			}
			yml, err := PrintObj(deployment, outputFormat)
			if err != nil {
				return "", err
			}
			manifest += yml
		}

		if out == OutputAll || out == OutputAllOther || out == OutputService {
			services, err := app.GetServices()
			if err != nil {
				return "", err
			}
			for _, service := range services {
				yml, err := PrintObj(service, outputFormat)
				if err != nil {
					return "", err
				}
				manifest += yml
			}
		}

		if out == OutputAll || out == OutputAllOther || out == OutputSecret {
			for _, ingressSecret := range app.GetIngressSecrets() {
				yml, err := PrintObj(ingressSecret, outputFormat)
				if err != nil {
					return "", err
				}
				manifest += yml
			}
		}

		if out == OutputAll || out == OutputAllOther || out == OutputIngress {
			ingress, err := app.GetIngress()
			if err != nil {
				return "", err
			}
			for _, ing := range ingress {
				yml, err := PrintObj(ing, outputFormat)
				if err != nil {
					return "", err
				}
				manifest += yml
			}
		}
	}

	return manifest, nil
}

// PrintObj return manifest from object
func PrintObj(obj runtime.Object, output string) (string, error) {
	if reflect.ValueOf(obj).IsNil() {
		return "", nil
	}

	printFlags := genericclioptions.NewPrintFlags("").WithTypeSetter(scheme.Scheme).WithDefaultOutput(output)

	printer, err := printFlags.ToPrinter()
	if err != nil {
		return "", err
	}

	out := bytes.NewBuffer([]byte{})
	if err := printer.PrintObj(obj, out); err != nil {
		return "", err
	}

	// remove 'creationTimestamp: null' from manifest
	filtered := bytes.NewBuffer([]byte{})
	for {
		line, err := out.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				break
			} else {
				return "", err
			}
		}
		if !bytes.Contains(line, []byte("creationTimestamp")) {
			filtered.Write(line)
		}
	}

	name := ""
	if acc, err := meta.Accessor(obj); err == nil {
		if n := acc.GetName(); len(n) > 0 {
			name = n
		}
	} else {
		return "", err
	}

	return fmt.Sprintf("---\n# %s: %s\n%s\n",
		reflect.Indirect(reflect.ValueOf(obj)).Type().Name(),
		name,
		filtered,
	), nil
}
