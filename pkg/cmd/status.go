package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/gosuri/uitable"
	"github.com/n0madic/app2kube/pkg/app2kube"
	"github.com/spf13/cobra"
	apiv1 "k8s.io/api/core/v1"
	metatable "k8s.io/apimachinery/pkg/api/meta/table"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	storageutil "k8s.io/kubectl/pkg/util/storage"
)

const maxColWidth = 63

type tableFunc struct {
	name string
	fn   func(*kubernetes.Clientset, string, map[string]string) (string, error)
}

// NewCmdStatus return App status in kubernetes
func NewCmdStatus() *cobra.Command {
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show application resources status in kubernetes",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := initApp()
			if err != nil {
				return err
			}

			cmd.SilenceUsage = true

			return status(app)
		},
	}

	addAppFlags(statusCmd)
	statusCmd.Flags().BoolVar(&flagAllInstances, "all-instances", false, "Show all instances of application")

	statusCmd.Flags().MarkHidden("include-namespace")

	return statusCmd
}

func status(app *app2kube.App) error {
	kcs, err := kubeFactory.KubernetesClientSet()
	if err != nil {
		return err
	}

	fmt.Printf("NAME: %s\n", app.GetReleaseName())
	fmt.Printf("NAMESPACE: %s\n\n", app.Namespace)

	fmt.Printf("RESOURCES:\n")

	tables := []tableFunc{
		{name: "ConfigMap", fn: getConfigmapStatus},
		{name: "Secret", fn: getSecretsStatus},
		{name: "CronJob", fn: getCronJobsStatus},
		{name: "PersistentVolumeClaim", fn: getPVCStatus},
		{name: "Deployment", fn: getDeploymentStatus},
		{name: "Pod (related)", fn: getPodsStatus},
		{name: "Service", fn: getServicesStatus},
		{name: "Ingress", fn: getIngressStatus},
	}

	for _, res := range tables {
		table, err := res.fn(kcs, app.Namespace, app.Labels)
		if err != nil {
			return err
		}
		if table != "" {
			fmt.Println("\n==>", res.name)
			fmt.Println(table)
		}
	}

	if len(app.Ingress) > 0 {
		fmt.Println()
		fmt.Println("Try the application URL:")

		for _, ingress := range app.Ingress {
			getURL := func(host, path string) string {
				https := ""
				if ingress.Letsencrypt || ingress.TLSSecretName != "" {
					https = "s"
				}
				return fmt.Sprintf("http%s://%s%s", https, host, path)
			}

			fmt.Println("  ", getURL(ingress.Host, ingress.Path))

			if app.Staging == "" {
				for _, alias := range ingress.Aliases {
					fmt.Println("  ", getURL(alias, ingress.Path))
				}
			}
		}
	}

	return nil
}

func getConfigmapStatus(kcs *kubernetes.Clientset, namespace string, labels map[string]string) (string, error) {
	list, err := kcs.CoreV1().ConfigMaps(namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: getSelector(labels),
	})
	if err != nil {
		return "", err
	}

	if len(list.Items) == 0 {
		return "", nil
	}

	table := uitable.New()
	table.MaxColWidth = maxColWidth
	table.AddRow("NAME", "DATA", "AGE")
	for _, configmap := range list.Items {
		table.AddRow(
			configmap.Name,
			len(configmap.Data)+len(configmap.BinaryData),
			metatable.ConvertToHumanReadableDateType(configmap.CreationTimestamp),
		)
	}

	return table.String(), nil
}

func getSecretsStatus(kcs *kubernetes.Clientset, namespace string, labels map[string]string) (string, error) {
	list, err := kcs.CoreV1().Secrets(namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: getSelector(labels),
	})
	if err != nil {
		return "", err
	}

	if len(list.Items) == 0 {
		return "", nil
	}

	table := uitable.New()
	table.MaxColWidth = maxColWidth
	table.AddRow("NAME", "DATA", "AGE")
	for _, secret := range list.Items {
		table.AddRow(
			secret.Name,
			len(secret.Data),
			metatable.ConvertToHumanReadableDateType(secret.CreationTimestamp),
		)
	}

	return table.String(), nil
}

func getCronJobsStatus(kcs *kubernetes.Clientset, namespace string, labels map[string]string) (string, error) {
	list, err := kcs.BatchV1beta1().CronJobs(namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: getSelector(labels),
	})
	if err != nil {
		return "", err
	}

	if len(list.Items) == 0 {
		return "", nil
	}

	table := uitable.New()
	table.MaxColWidth = maxColWidth
	table.AddRow("NAME", "SCHEDULE", "SUSPEND", "ACTIVE", "LAST SCHEDULE", "AGE")
	for _, cron := range list.Items {
		var lastScheduleTime string
		if cron.Status.LastScheduleTime != nil {
			lastScheduleTime = cron.Status.LastScheduleTime.String()
		}
		table.AddRow(
			cron.Name,
			cron.Spec.Schedule,
			*cron.Spec.Suspend,
			len(cron.Status.Active),
			lastScheduleTime,
			metatable.ConvertToHumanReadableDateType(cron.CreationTimestamp),
		)
	}

	return table.String(), nil
}

func getPVCStatus(kcs *kubernetes.Clientset, namespace string, labels map[string]string) (string, error) {
	list, err := kcs.CoreV1().PersistentVolumeClaims(namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: getSelector(labels),
	})
	if err != nil {
		return "", err
	}

	if len(list.Items) == 0 {
		return "", nil
	}

	table := uitable.New()
	table.MaxColWidth = maxColWidth
	table.AddRow("NAME", "STATUS", "VOLUME", "CAPACITY", "ACCESS MODES", "STORAGECLASS", "AGE")
	for _, pvc := range list.Items {
		capacity := pvc.Status.Capacity[apiv1.ResourceStorage]
		table.AddRow(
			pvc.Name,
			pvc.Status.Phase,
			pvc.Spec.VolumeName,
			capacity.String(),
			storageutil.GetAccessModesAsString(pvc.Status.AccessModes),
			storageutil.GetPersistentVolumeClaimClass(&pvc),
			metatable.ConvertToHumanReadableDateType(pvc.CreationTimestamp),
		)
	}

	return table.String(), nil
}

func getDeploymentStatus(kcs *kubernetes.Clientset, namespace string, labels map[string]string) (string, error) {
	serviceColor, _ := getCurrentBlueGreenColor(namespace, labels)

	list, err := kcs.AppsV1().Deployments(namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: getSelector(labels),
	})
	if err != nil {
		return "", err
	}

	if len(list.Items) == 0 {
		return "", nil
	}

	table := uitable.New()
	table.MaxColWidth = maxColWidth
	table.AddRow("NAME", "READY", "UP-TO-DATE", "AVAILABLE", "AGE")
	for _, deployment := range list.Items {
		activeMark := ""
		currentColor := deployment.Spec.Selector.MatchLabels["app.kubernetes.io/color"]
		if currentColor == serviceColor && len(list.Items) > 1 && !flagAllInstances {
			activeMark = "*"
		}
		if currentColor != "" {
			deployment.Name = colorize(currentColor, deployment.Name)
		}
		ready := fmt.Sprintf("%d/%d", deployment.Status.ReadyReplicas, deployment.Status.Replicas)
		table.AddRow(
			deployment.Name+activeMark,
			ready,
			deployment.Status.UpdatedReplicas,
			deployment.Status.AvailableReplicas,
			metatable.ConvertToHumanReadableDateType(deployment.CreationTimestamp),
		)
	}

	return table.String(), nil
}

func getPodsStatus(kcs *kubernetes.Clientset, namespace string, labels map[string]string) (string, error) {
	list, err := kcs.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: getSelector(labels),
	})
	if err != nil {
		return "", err
	}

	if len(list.Items) == 0 {
		return "", nil
	}

	table := uitable.New()
	table.MaxColWidth = maxColWidth
	table.AddRow("NAME", "PHASE", "STATUS", "RESTARTS", "AGE")
	for _, pod := range list.Items {
		currentColor := pod.ObjectMeta.Labels["app.kubernetes.io/color"]
		if currentColor != "" {
			pod.Name = colorize(currentColor, pod.Name)
		}
		var readyCount, restartCount int
		for _, container := range pod.Status.ContainerStatuses {
			restartCount += int(container.RestartCount)
			if container.Ready && container.State.Running != nil {
				readyCount++
			}
		}
		reason := string(pod.Status.Phase)
		if pod.Status.Reason != "" {
			reason = pod.Status.Reason
		}
		if pod.DeletionTimestamp != nil && pod.Status.Reason == "NodeLost" {
			reason = "Unknown"
		} else if pod.DeletionTimestamp != nil {
			reason = "Terminating"
		}
		table.AddRow(
			pod.Name,
			reason,
			fmt.Sprintf("%d/%d", readyCount, len(pod.Spec.Containers)),
			restartCount,
			metatable.ConvertToHumanReadableDateType(pod.CreationTimestamp),
		)
	}

	return table.String(), nil
}

func getServicesStatus(kcs *kubernetes.Clientset, namespace string, labels map[string]string) (string, error) {
	list, err := kcs.CoreV1().Services(namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: getSelector(labels),
	})
	if err != nil {
		return "", err
	}

	if len(list.Items) == 0 {
		return "", nil
	}

	table := uitable.New()
	table.MaxColWidth = maxColWidth
	table.AddRow("NAME", "TYPE", "CLUSTER-IP", "EXTERNAL-IP", "PORT(S)", "AGE")
	for _, svc := range list.Items {
		externalIPs := "<none>"
		if len(svc.Spec.ExternalIPs) > 0 {
			externalIPs = strings.Join(svc.Spec.ExternalIPs, ",")
		}
		var ports []string
		for _, port := range svc.Spec.Ports {
			ports = append(ports, strconv.Itoa(int(port.Port)))
		}
		currentColor := svc.Spec.Selector["app.kubernetes.io/color"]
		if currentColor != "" {
			svc.Name = colorize(currentColor, svc.Name)
		}
		table.AddRow(
			svc.Name,
			svc.Spec.Type,
			svc.Spec.ClusterIP,
			externalIPs,
			strings.Join(ports, ","),
			metatable.ConvertToHumanReadableDateType(svc.CreationTimestamp),
		)
	}

	return table.String(), nil
}

func getIngressStatus(kcs *kubernetes.Clientset, namespace string, labels map[string]string) (string, error) {
	list, err := kcs.NetworkingV1beta1().Ingresses(namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: getSelector(labels),
	})
	if err != nil {
		return "", err
	}

	if len(list.Items) == 0 {
		return "", nil
	}

	table := uitable.New()
	table.MaxColWidth = 100
	table.AddRow("NAME", "HOSTS", "AGE")
	for _, ingress := range list.Items {
		hosts := []string{}
		for _, rule := range ingress.Spec.Rules {
			hosts = append(hosts, rule.Host)
		}
		table.AddRow(
			ingress.Name,
			strings.Join(hosts, ","),
			metatable.ConvertToHumanReadableDateType(ingress.CreationTimestamp),
		)
	}

	return table.String(), nil
}
