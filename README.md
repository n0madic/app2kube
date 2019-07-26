# app2kube

The easiest way to create kubernetes manifests for an application

## Install

Download binaries from [release](https://github.com/n0madic/app2kube/releases) page.

Or install from source:

```shell
go get -u github.com/n0madic/app2kube/cmd/app2kube
```

## Help

```
Usage:
  app2kube [flags]

Flags:
  -h, --help                     help for app2kube
  -i, --ingress string           Ingress class (default "nginx")
  -n, --namespace string         Namespace used for manifests
      --set stringArray          Set values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2)
      --set-file stringArray     Set values from respective files specified via the command line (can specify multiple or separate values with commas: key1=path1,key2=path2)
      --set-string stringArray   Set STRING values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2)
  -s, --snapshot string          Save the merged YAML values in the specified file for reuse
  -f, --values valueFiles        Specify values in a YAML file or a URL (can specify multiple) (default [])
  -v, --verbose                  Show the merged YAML values as well
```

## Usage

Minimum required values for application deployment in kubernetes - `name` and `image`.


```shell
app2kube --set name=example --set deployment.containers.example.image=example/image:latest
```

OR

`values.yaml`:
```yaml
---
name: example
deployment:
  containers:
    example:
      image: "example/image:latest"
```


```shell
app2kube -f values.yaml | kubectl apply -f -
```

## Examples

Simple web service:

```yaml
---
name: example
deployment:
  containers:
    example:
      image: "example/image:latest"
      livenessProbe:
        tcpSocket:
          port: 8080
      readinessProbe:
        httpGet:
          port: 8080
          path: /healthz
  service:
  - name: http
    port: 8080
  ingress:
  - host: "example.com"
```

PHP-FPM application with web service (common image) and cron jobs, include memcached:

```yaml
---
name: example
common:
  image:
    repository: "example/php-nginx"
    tag: "7.3"
cronjob:
  update:
    schedule: "0 */1 * * *"
    containers:
      update:
        command: ["/usr/bin/curl"]
        args: ["https://www.example.com/update"]
  cleaning:
    schedule: "0 0 * * *"
    containers:
      cleaning:
        command: ["/usr/bin/php"]
        args: ["/var/www/cron.php", "--cleaning"]
deployment:
  containers:
    php-fpm:
      command: ["/usr/sbin/php-fpm7.3"]
      args: ["--nodaemonize", "--force-stderr"]
      resources:
        requests:
          memory: 350Mi
          cpu: 10m
      livenessProbe:
        tcpSocket:
          port: 9000
    nginx:
      command: ["/usr/sbin/nginx"]
      args: ["-g", "daemon off;"]
      lifecycle:
        preStop:
          exec:
            command: ["/usr/sbin/nginx", "-s"," quit"]
      livenessProbe:
        tcpSocket:
          port: 80
      readinessProbe:
        httpGet:
          port: 80
          path: /
        timeoutSeconds: 60
    memcached:
      image: "memcached:alpine"
      imagePullPolicy: IfNotPresent
      livenessProbe:
        tcpSocket:
          port: 11211
  service:
  - name: http
    port: 80
  ingress:
  - host: "example.com"
    aliases:
    - "www.example.com"
    letsencrypt: true
    sslRedirect: true
```
