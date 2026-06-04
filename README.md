# app2kube

The easiest way to create and apply kubernetes manifests for an application

## Features

* Simple deployment to kubernetes without knowledge of manifest syntax
* There are no built-in manifest templates under the hood, only native kubernetes objects
* Understandable set of values for configuration in YAML
* Templating values in YAML file with [sprig](http://masterminds.github.io/sprig/) functions
* Safe-by-default manifests: an automatic liveness probe and a conservative `securityContext` (all overridable, see [Defaults and hardening](#defaults-and-hardening))
* Supported Kubernetes resources:
  * ConfigMap
  * CronJob
  * Deployment
  * Ingress
  * Namespace
  * PersistentVolumeClaim
  * Secret
  * Service
* Secret value encryption with AES-256 GCM or RSA-2048
* Support staging
* Build and push docker image
* Apply/delete a configuration to a resource in kubernetes
* Track application deployment in kubernetes
* Blue/green deployment
* Portable - `apply`/`delete` command ported from kubectl, `build` from docker-cli

## Install

Download binaries from [release](https://github.com/n0madic/app2kube/releases) page.

Or install from source:

```shell
go get -u github.com/n0madic/app2kube
```

## Help

```help
Usage:
  app2kube [command]

Available Commands:
  apply       Apply a configuration to a resource in kubernetes
  blue-green  Commands for blue-green deployment
  build       Build and push an image from a Dockerfile
  completion  Generates bash completion scripts
  config      Manage application config
  delete      Delete resources from kubernetes
  help        Help about any command
  manifest    Generate kubernetes manifests for an application
  status      Show application resources status in kubernetes
  track       Track application deployment in kubernetes

Flags:
      --certificate-authority string   Path to a cert file for the certificate authority
      --client-certificate string      Path to a client certificate file for TLS
      --client-key string              Path to a client key file for TLS
      --cluster string                 The name of the kubeconfig cluster to use
      --context string                 The name of the kubeconfig context to use
  -h, --help                           help for app2kube
      --insecure-skip-tls-verify       If true, the server's certificate will not be checked for validity. This will make your HTTPS connections insecure
      --kubeconfig string              Path to the kubeconfig file to use for CLI requests.
  -n, --namespace string               If present, the namespace scope for this CLI request
      --request-timeout string         The length of time to wait before giving up on a single server request. Non-zero values should contain a corresponding time unit (e.g. 1s, 2m, 3h). A value of zero means don't timeout requests. (default "0")
  -s, --server string                  The address and port of the Kubernetes API server
      --token string                   Bearer token for authentication to the API server
      --user string                    The name of the kubeconfig user to use
      --version                        version for app2kube
```

## Usage

Minimum required values for application deployment in kubernetes - `name` and `image`.

```shell
app2kube manifest --set name=example --set deployment.containers.example.image=example/image:latest
```

By default, it tries to use the `.app2kube.yml` file in the current directory:

```yaml
---
name: example
deployment:
  containers:
    example:
      image: "example/image:latest"
```

OR

```yaml
---
name: example
common:
  image:
    repository: "example/image"
    tag: "latest"
deployment:
  containers:
    example: {}
```

Get kubernetes manifest:

```shell
app2kube manifest
```

Build and push docker image:

```shell
app2kube build --push
```

Apply application manifest in kubernetes:

```shell
app2kube apply
```

Track deployment till ready:

```shell
app2kube track ready
```

Delete application manifest from kubernetes:

```shell
app2kube delete
```

## Secrets

The `secrets:` section of the YAML file can store secrets, which can be encrypted using either AES-256 GCM or RSA-2048. To use AES-256 GCM, set the `APP2KUBE_PASSWORD` environment variable. For RSA-2048, set the `APP2KUBE_ENCRYPT_KEY` or `APP2KUBE_DECRYPT_KEY` environment variable. If both are set, RSA will take precedence over AES. Secrets encrypted with the legacy AES-256 CBC format are still decrypted transparently for backward compatibility.

To encrypt YAML file:
```shell
app2kube config encrypt --values secrets.yml
```

To encrypt a single value:
```shell
app2kube config encrypt --string "secret"
```

Passing the plaintext on the command line lands it in your shell history and `ps` output. To avoid that, use `--string -` to read the value from stdin:
```shell
printf '%s' "secret" | app2kube config encrypt --string -
```

Encrypted values are prefixed with `AES#` or `RSA#` depending on the encryption algorithm. It is possible to use both encryption algorithms in one file.

To decrypt, set environment variable `APP2KUBE_PASSWORD` or `APP2KUBE_DECRYPT_KEY`.
```shell
app2kube config secrets
```
`config secrets` and `config dotenv` print **decrypted** secrets to stdout — redirect them with care.

## Staging

For a staging release, you must set the `staging` and optional `branch` parameters:

```shell
app2kube manifest --set staging=alpha --set branch=develop
```

In this case, some values will be reset to more optimal for staging:

```yaml
common:
  image:
    pullPolicy: Always
deployment:
  blueGreenColor: ""
  replicaCount: 1
  revisionHistoryLimit: 0
```

Also, aliases for ingress and resource requests for containers (for a denser filling of the staging environment) will not be used.

For ingress domains, prefixes from the above values will be automatically added: `staging.example.com` or `branch.staging.example.com`

## Defaults and hardening

To produce safe-by-default manifests, app2kube fills in a few fields when you do not set them explicitly. All of them are overridable.

**Probes.** A container that exposes exactly one port and has no `livenessProbe` gets a TCP liveness probe on that port. A `readinessProbe` is **not** auto-created — readiness gates Service traffic and rollout progress, so it is left to explicit configuration; an existing readiness probe only has its missing `httpGet` port filled in. Init containers never receive auto probes.

**Container securityContext.** App-image containers (those built from `common.image`) that declare no `securityContext` get a conservative, non-breaking default:

```yaml
securityContext:
  allowPrivilegeEscalation: false
```

Capabilities are intentionally **not** dropped: dropping `ALL` breaks common workloads that rely on default Linux capabilities (a root `nginx` binding `:80` needs `NET_BIND_SERVICE`, and its master needs `SETUID`/`SETGID` to spawn workers), and no single minimal add-set fits every image. Drop capabilities explicitly via the container's own `securityContext` where you know what a workload needs. Third-party sidecar images are left untouched. Set a container's own `securityContext` to override (an explicit `securityContext: {}` opts out). The more disruptive `runAsNonRoot` / `readOnlyRootFilesystem` are intentionally not defaulted — enable them where appropriate.

**Pod securityContext.** The pod template gets `seccompProfile: { type: RuntimeDefault }` by default. Provide `common.securityContext` (a full Kubernetes `PodSecurityContext`) to take over completely — for example, to enforce non-root:

```yaml
common:
  securityContext:
    runAsNonRoot: true
    runAsUser: 1000
```

An explicit `common.securityContext: {}` opts out of the seccomp default.

**Resources.** Set `common.resources` to apply a single baseline `resources` block to every app-image container that defines none (so pods are not `BestEffort` and pass a `LimitRange`):

```yaml
common:
  resources:
    requests:
      cpu: 10m
      memory: 32Mi
```

Per-container `resources` always win. In staging, container resources are still stripped (see [Staging](#staging)).

**Labels and selectors.** Every generated object carries the recommended labels `app.kubernetes.io/name`, `app.kubernetes.io/instance` (default `production`, or the staging name) and `app.kubernetes.io/managed-by=app2kube`. These are set by the library itself, so manifests built programmatically (not only via the CLI) are selectable by the prune/delete tooling. Any extra keys under `labels:` are merged in and propagate to objects and pod templates.

The Deployment's `spec.selector` carries the full label set — `name`, `instance`, `managed-by` and any user labels (plus `app.kubernetes.io/color` for blue/green) — byte-identical to the pod template labels. `spec.selector` is immutable in Kubernetes, so keeping it identical to what every app2kube version has emitted lets a plain `kubectl apply` upgrade existing Deployments in place. The Service and `PodDisruptionBudget` selectors use the same set, so all three stay consistent.

**Init containers and shared data.** App-image init containers inherit the same injected configuration as the main app containers — the global `env`, the `envFrom` references to the release ConfigMap/Secret, the PVC mounts, and the `common.sharedData` mount — so an init step (e.g. a migration) sees the app's config without repeating it. When `common.sharedData` is set, the `shared-data` `emptyDir` volume is always emitted (even for a single container) so every mount has a matching volume. Third-party sidecar/init images are never injected into.

**Config rollout.** Kubernetes does not restart pods when a ConfigMap/Secret consumed via `envFrom` changes, so an `apply` of changed config would otherwise leave pods running stale values. To fix this, the pod template of any workload that actually references the config gets `checksum/configmap` and/or `checksum/secret` annotations (a sha256 of the referenced data). Changing a `configmap:`/`secrets:` value changes the checksum, which changes the pod template, so `kubectl apply` rolls the Deployment (and cronjob pods). Workloads that do not consume the config — e.g. a pod built only from a third-party image — get no checksum and are never rolled by an unrelated change. The secret checksum is taken over the stored value (ciphertext or plaintext), so rendering never requires the decrypt key.

**Persistent volumes.** Each `volumes:` entry must set `spec.accessModes` (an empty value yields a PVC the apiserver rejects, so app2kube fails fast with a clear error). A PVC is mounted into the **Deployment**, so a `ReadWriteOnce` volume mounted into a multi-replica Deployment cannot be shared across nodes and scheduling blocks — app2kube warns about this on stderr. Use a single replica or a `ReadWriteMany` volume; generating a `StatefulSet` is out of scope.

**Services.** For a `NodePort` service the requested external port is pinned as the node port only when it falls inside the valid range `30000-32767`; an out-of-range value is left for the apiserver to auto-assign and a warning is printed to stderr (rather than silently dropping it). When several `ingress:` entries share the same host they are merged into one Ingress object; because `ingressClassName` is ingress-wide, two entries for the same host requesting **different** classes is an error.

**Service account.** `automountServiceAccountToken` defaults to `false`. If you set `common.mountServiceAccountToken: true` without a dedicated account, the pod mounts the namespace **default** ServiceAccount token, which often has broader access than intended — set `common.serviceAccountName` to bind a least-privilege account instead.

**Namespace precedence.** The namespace is resolved as `--namespace` flag > value-file `namespace:` > `default`. An explicitly-set `--namespace` wins even when empty, so `--namespace ""` forces the `default` namespace over a value-file setting.

**Image pull policy.** When `image.pullPolicy` is unset, app2kube sets it explicitly (instead of relying on Kubernetes' version-specific implicit rule) so deploys are reproducible: an image tagged `:latest`, with no tag, defaults to `Always`; a fixed tag or a digest-pinned image (`@sha256:...`) defaults to `IfNotPresent`. A `:latest` common image in a non-staging deploy also prints a stderr warning — pin a specific tag or digest for reproducible rollouts.

**Rollout strategy.** When `deployment.strategy` is unset it is left empty, so Kubernetes applies its built-in `RollingUpdate` default (`maxUnavailable`/`maxSurge` 25%). `deployment.progressDeadlineSeconds` defaults to 15 minutes (`900`) — matching the default deploy tracking timeout — so a wedged rollout reports failure instead of hanging. Both are overridable.

**Disruption budget.** When the Deployment runs more than one replica, app2kube emits a `PodDisruptionBudget` with `minAvailable: 1` (rendered with the Deployment, also selectable via `--type pdb`) so a node drain/upgrade cannot evict all replicas at once. A single-replica deploy gets none — a `minAvailable: 1` PDB would block every drain — and therefore has no voluntary-disruption protection.

## Examples

Simple web service:

```yaml
---
name: example
deployment:
  containers:
    example:
      image: "example/image:latest"
      ports:
      - containerPort: 8080
        name: http
      readinessProbe:
        httpGet:
          path: /healthz
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
  cleaning:
    schedule: "0 0 * * *"
    container:
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
  http:
    port: 80
ingress:
- host: "example.com"
  aliases:
  - "www.example.com"
  letsencrypt: true
  sslRedirect: true
```
