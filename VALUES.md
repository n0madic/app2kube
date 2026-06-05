# app2kube YAML values reference

This document describes every value app2kube reads from its YAML configuration
(`.app2kube.yml` by default, or files passed with `-f/--values`). Values are
unmarshaled into the `App` struct in [`pkg/app2kube/app.go`](pkg/app2kube/app.go),
which is the authoritative source for this reference.

Minimum required configuration is `name` plus at least one container image
(either `deployment.containers.<name>.image` or `common.image.repository`).

## Table of contents

- [How values are loaded](#how-values-are-loaded)
- [Top-level values](#top-level-values)
- [`common`](#common) — settings shared by all workloads
- [`deployment`](#deployment) — the main Deployment
- [`cronjob`](#cronjob) — scheduled jobs
- [`service`](#service) — cluster Services
- [`ingress`](#ingress) — HTTP routing and TLS
- [`volumes`](#volumes) — PersistentVolumeClaims
- [`configmap` / `env`](#configmap--env) — non-secret configuration
- [`secrets`](#secrets) — secret configuration
- [`labels`](#labels) — object labels and selectors
- [Container spec](#container-spec) — fields under `*.containers.<name>`
- [Defaults and hardening](#defaults-and-hardening)
- [Staging overrides](#staging-overrides)
- [Non-obvious behaviors](#non-obvious-behaviors)
- [Annotated example](#annotated-example)
- [Full reference (every field)](#full-reference-every-field)

---

## How values are loaded

Values come from four sources, merged in this order (later sources win):

1. **Value files** — `-f/--values file.yaml` (repeatable, comma-separated).
   Read top-to-bottom; a later file deep-merges over an earlier one.
2. **`--set key=value`** — typed inline overrides (parsed like Helm `--set`).
3. **`--set-string key=value`** — same, but the value is always a string.
4. **`--set-file key=path`** — the value is read from a file's contents.

Additional loading behavior:

| Feature | Syntax | Notes |
|---|---|---|
| Read from stdin | `-f -` | Reads the value file from standard input. |
| Optional value file | `-f file.yaml?` | Trailing `?` — a missing file warns instead of failing. |
| Templating | Go `text/template` + [sprig](http://masterminds.github.io/sprig/) | Each value file is rendered as a template **before** YAML parsing. Template data is `nil`, so templating is used for sprig helper functions (e.g. `{{ env "VAR" }}`, `{{ now }}`), not for referencing other values. |
| Deep merge | — | Maps are merged key-by-key; scalars and lists are overwritten wholesale. |

Unknown top-level keys are ignored during unmarshaling.

---

## Top-level values

These keys live at the root of the YAML document.

| Key | Type | Default | Description |
|---|---|---|---|
| `name` | string | — (**required**) | Application name. Lowercased and `_`→`-` normalized. Backs object names and the `app.kubernetes.io/name` label. |
| `namespace` | string | resolved (see below) | Target namespace. A `Namespace` object is emitted only when this is non-empty. |
| `staging` | string | `""` | Staging environment name. When set, triggers [staging overrides](#staging-overrides). |
| `branch` | string | `""` | Branch name, used together with `staging` for instance labels and ingress host prefixes. |
| `labels` | map[string]string | see [`labels`](#labels) | Extra labels merged onto every object and pod template. |
| `env` | map[string]string | `{}` | Plain environment variables injected into all app-image containers (see [`configmap`/`env`](#configmap--env)). |
| `configmap` | map[string]string | `{}` | Non-secret config rendered as a `ConfigMap` and wired via `envFrom`. |
| `secrets` | map[string]string | `{}` | Secret config rendered as a `Secret` and wired via `envFrom`. Values may be encrypted (see [`secrets`](#secrets)). |
| `common` | object | — | Settings shared by all workloads — see [`common`](#common). |
| `deployment` | object | — | The main Deployment — see [`deployment`](#deployment). |
| `cronjob` | map[string]object | `{}` | Named scheduled jobs — see [`cronjob`](#cronjob). |
| `service` | map[string]object | `{}` | Named cluster Services — see [`service`](#service). |
| `ingress` | list of objects | `[]` | HTTP routing rules — see [`ingress`](#ingress). |
| `volumes` | map[string]object | `{}` | PersistentVolumeClaims — see [`volumes`](#volumes). |

**Namespace precedence:** `--namespace` flag > value-file `namespace:` > `default`.
An explicitly set `--namespace` wins even when empty, so `--namespace ""` forces
the `default` namespace over a value-file setting.

---

## `common`

Settings shared by the Deployment and every CronJob pod template.

| Key | Type | Default | Description |
|---|---|---|---|
| `common.image.repository` | string | `""` | Image repository for the "app image". Containers without their own `image` inherit `repository:tag`. |
| `common.image.tag` | string | `latest` | Tag for the app image. An explicit empty value is restored to `latest`. |
| `common.image.pullPolicy` | string | computed | `Always` / `IfNotPresent` / `Never`. When unset, computed per image: `Always` for `:latest`/untagged, `IfNotPresent` for a fixed tag or `@sha256:` digest. Forced to `Always` in staging. |
| `common.image.pullSecrets` | string | `""` | Name of an `imagePullSecrets` reference added to every pod. |
| `common.ingress` | object | — | Ingress defaults applied to all `ingress[]` entries — see [`ingress`](#ingress). |
| `common.resources` | [ResourceRequirements](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/) | `nil` | Baseline `requests`/`limits` applied to every app-image container that declares none. Per-container `resources` always win. Ignored in staging. |
| `common.securityContext` | [PodSecurityContext](https://kubernetes.io/docs/tasks/configure-pod-container/security-context/) | `nil` | Full pod-level security context. When unset, app2kube emits `seccompProfile: RuntimeDefault`. An explicit `{}` opts out of that default. |
| `common.nodeSelector` | map[string]string | `{}` | Pod `nodeSelector`. |
| `common.tolerations` | list of [Toleration](https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration/) | `[]` | Pod tolerations. |
| `common.podAntiAffinity` | string | `""` | `preferred` or `required` (case-insensitive). Generates pod anti-affinity across `kubernetes.io/hostname` on the app's labels (excluding `managed-by`). Any other non-empty value is an error. |
| `common.dnsPolicy` | string | `""` (k8s `ClusterFirst`) | Pod [DNS policy](https://kubernetes.io/docs/concepts/services-networking/dns-pod-service/#pod-s-dns-policy). |
| `common.enableServiceLinks` | bool | `false` | Sets pod `enableServiceLinks`. **Note:** Kubernetes defaults this to `true`; app2kube emits `false` unless you set it. |
| `common.gracePeriod` | int64 (seconds) | `0` (k8s `30`) | Pod `terminationGracePeriodSeconds`. Only emitted when `> 0`. |
| `common.sharedData` | string | `""` | Mount path of a `shared-data` `emptyDir` shared by all app-image containers (main + init). The volume is emitted whenever this is set. |
| `common.serviceAccountName` | string | `""` | Pod `serviceAccountName`. |
| `common.mountServiceAccountToken` | bool | `false` | Sets pod `automountServiceAccountToken`. Enabling without `serviceAccountName` mounts the namespace **default** SA token. |
| `common.cronjobSuspend` | bool | `false` | When `true`, every generated CronJob is created with `suspend: true`. |

---

## `deployment`

The application's primary Deployment. A Deployment is emitted only when
`deployment.containers` is non-empty.

| Key | Type | Default | Description |
|---|---|---|---|
| `deployment.containers` | map[string][Container](#container-spec) | `{}` | Main containers, keyed by name (lowercased). |
| `deployment.initContainers` | map[string][Container](#container-spec) | `{}` | Init containers. They inherit the app's injected config but never get auto probes. |
| `deployment.replicaCount` | int32 (pointer) | `1` | Replica count. An explicit `0` (scale-to-zero) is honored and distinguished from unset. Negative values are clamped to `0`. |
| `deployment.replicaCountStaging` | int32 | `0` | Replica count used instead of `1` when `staging` is set and this is `> 0`. |
| `deployment.revisionHistoryLimit` | int32 | `2` | Deployment `revisionHistoryLimit`. Forced to `0` in staging. |
| `deployment.progressDeadlineSeconds` | int32 (pointer) | `900` (15 min) | Deployment `progressDeadlineSeconds`, matching the default deploy-tracking timeout so a wedged rollout reports failure. |
| `deployment.strategy` | [DeploymentStrategy](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/#strategy) | `{}` (k8s `RollingUpdate` 25%/25%) | Rollout strategy. Left empty when unset so Kubernetes applies its built-in default. |
| `deployment.blueGreenColor` | string | `""` | Blue/green color suffix. Adds the `app.kubernetes.io/color` label and a `-<color>` name suffix. Cleared in staging. |

---

## `cronjob`

A map of named scheduled jobs. Each key becomes a CronJob name
(`<release>-<key>`, lowercased, capped at 52 characters). Two keys that collapse
to the same object name are a fatal error.

| Key | Type | Default | Description |
|---|---|---|---|
| `cronjob.<name>.schedule` | string | — (**required**) | Cron schedule expression. |
| `cronjob.<name>.container` | [Container](#container-spec) | — | A single job container. When specified it **must** set a `command` (else it is an error); its name defaults to `<name>-job`. |
| `cronjob.<name>.containers` | map[string][Container](#container-spec) | `{}` | Multiple named job containers. Can be combined with `container`. |
| `cronjob.<name>.concurrencyPolicy` | string | `""` (k8s `Allow`) | `Allow` / `Forbid` / `Replace`. |
| `cronjob.<name>.restartPolicy` | string | `Never` | Pod `restartPolicy` (`Never` / `OnFailure`). |
| `cronjob.<name>.suspend` | bool | `false` | Suspend this CronJob. Overridden to `true` by `common.cronjobSuspend`. |
| `cronjob.<name>.timeZone` | string | `""` (cluster local) | IANA time zone for the schedule (e.g. `America/Los_Angeles`). |
| `cronjob.<name>.backoffLimit` | int32 (pointer) | `6` | Job `backoffLimit`. |
| `cronjob.<name>.activeDeadlineSeconds` | int64 (pointer) | `86400` (1 day) | Job `activeDeadlineSeconds`. |
| `cronjob.<name>.failedJobsHistoryLimit` | int32 (pointer) | `2` | Kept failed Jobs. |
| `cronjob.<name>.successfulJobsHistoryLimit` | int32 (pointer) | `2` | Kept successful Jobs. |

**Containers.** `container` and `containers` coexist: the single `container`
(named `<name>-job`) is emitted first, then the `containers` entries in sorted
key order. A cronjob that ends up with no containers (neither field populated) is
an error.

---

## `service`

A map of named cluster Services, emitted only when `deployment.containers`
exists. Each key becomes the Service name suffix (`<release>-<key>`, or the bare
release name for an empty key).

| Key | Type | Default | Description |
|---|---|---|---|
| `service.<name>.port` | int32 | `0` | Convenience port: fills both internal and external when they are unset. |
| `service.<name>.internalPort` | int32 | `0` | `targetPort` (container port). Falls back to `externalPort`/`port`. |
| `service.<name>.externalPort` | int32 | `0` | Service `port`. Falls back to `internalPort`/`port`. |
| `service.<name>.protocol` | string | `""` (k8s `TCP`) | `TCP` / `UDP` / `SCTP`. |
| `service.<name>.type` | string | `""` (k8s `ClusterIP`) | `ClusterIP` / `NodePort` / `LoadBalancer` / `ExternalName`. |

At least one of `port`/`internalPort`/`externalPort` must resolve to a non-zero
value. For a `NodePort`, the requested `externalPort` is pinned as the node port
only when it falls inside `30000–32767`; otherwise it is left for the apiserver
to auto-assign (with a warning).

> If a single container with named ports is the only container and an `ingress`
> exists but no `service` is defined, a Service is auto-created from the
> container's named ports.

---

## `ingress`

A **list** of HTTP routing rules, emitted only when both `deployment.containers`
and `service` exist. Entries sharing the same `host` are merged into one Ingress
object.

| Key | Type | Default | Description |
|---|---|---|---|
| `ingress[].host` | string | — | Primary hostname. With `staging`, prefixed as `staging.host` / `branch.staging.host`. A leading `*` wildcard is invalid under staging. |
| `ingress[].aliases` | list of string | `[]` | Additional hostnames routed to the same backend. Suppressed under staging. |
| `ingress[].path` | string | `/` | HTTP path (`PathType: ImplementationSpecific`). |
| `ingress[].class` | string | `nginx` | `ingressClassName`. Resolves to `common.ingress.class` then `nginx`. Two entries for the same host requesting different classes is an error. |
| `ingress[].serviceName` | string | derived | Backend Service. When empty and exactly one Service exists, it is used; otherwise this is required. |
| `ingress[].servicePort` | int32 | derived | Backend port. Derived from the referenced Service when not given explicitly. |
| `ingress[].letsencrypt` | bool | `false` | Adds `kubernetes.io/tls-acme: "true"` and emits a placeholder TLS Secret for cert-manager. |
| `ingress[].sslRedirect` | bool | `false` | Sets `nginx.ingress.kubernetes.io/ssl-redirect` when TLS is active. |
| `ingress[].annotations` | map[string]string | `{}` | Extra Ingress annotations (merged after `common.ingress.annotations`). |
| `ingress[].tlsCrt` | string | `""` | Inline PEM certificate. With `tlsKey`, emits a `kubernetes.io/tls` Secret. |
| `ingress[].tlsKey` | string | `""` | Inline PEM private key (paired with `tlsCrt`). |
| `ingress[].tlsSecretName` | string | `tls-<host>` | Override the TLS Secret name. |

`common.ingress` provides defaults applied to every entry:

| Key | Type | Default | Description |
|---|---|---|---|
| `common.ingress.class` | string | `""` | Default `ingressClassName` (falls through to `nginx`). |
| `common.ingress.annotations` | map[string]string | `{}` | Annotations merged into every Ingress (entry-level overrides win). |
| `common.ingress.letsencrypt` | bool | `false` | Enable ACME/TLS for all entries. |
| `common.ingress.sslRedirect` | bool | `false` | Enable SSL redirect for all entries. |
| `common.ingress.serviceName` | string | `""` | Default backend Service name. |
| `common.ingress.servicePort` | int32 | `0` | Default backend Service port. |

---

## `volumes`

A map of named PersistentVolumeClaims. Each key becomes the PVC name
(`<release>-<key>`) and the pod volume name, and is mounted into the Deployment
(and any cron container) at `mountPath`.

| Key | Type | Default | Description |
|---|---|---|---|
| `volumes.<name>.mountPath` | string | — (**required**) | Mount path inside app-image containers. |
| `volumes.<name>.spec` | [PersistentVolumeClaimSpec](https://kubernetes.io/docs/concepts/storage/persistent-volumes/#persistentvolumeclaims) | — (**required**) | Full PVC spec. `accessModes` is required (an empty value is rejected by the apiserver, so app2kube fails fast). |

> A `ReadWriteOnce` volume mounted into a multi-replica Deployment cannot be
> shared across nodes; app2kube warns on stderr. Use a single replica or a
> `ReadWriteMany` volume.

Example:

```yaml
volumes:
  data:
    mountPath: /var/lib/data
    spec:
      accessModes:
        - ReadWriteOnce
      storageClassName: slow
      resources:
        requests:
          storage: 8Gi
```

---

## `configmap` / `env`

Both inject non-secret configuration into **app-image containers only**
(third-party-image containers are left untouched).

| Key | Mechanism | Description |
|---|---|---|
| `env` | direct `env:` entries | Each key/value becomes a container env var. A container's own `env` entry of the same name wins (the global one is skipped). |
| `configmap` | `ConfigMap` + `envFrom` | Rendered as a `ConfigMap` named after the release; referenced via `envFrom`. A content change updates the `checksum/configmap` pod annotation, rolling the workload. |

---

## `secrets`

Like `configmap`, but rendered as a `Secret` and wired via `envFrom`. Values may
be stored in plaintext or encrypted. A change rolls the workload via the
`checksum/secret` annotation (computed over the stored value, so rendering never
needs the decrypt key).

| Prefix | Algorithm | Key/env |
|---|---|---|
| _(none)_ | plaintext | — |
| `AES#` | AES-256 GCM | `APP2KUBE_PASSWORD` |
| `CRYPT#` | AES-256 CBC (legacy) | `APP2KUBE_PASSWORD` (decrypt only, backward-compat) |
| `RSA#` | RSA-2048 | `APP2KUBE_ENCRYPT_KEY` / `APP2KUBE_DECRYPT_KEY` |

If both AES and RSA keys are set, RSA takes precedence. Encrypt with
`app2kube config encrypt --values secrets.yml` or a single value with
`app2kube config encrypt --string -` (reads stdin to keep it out of shell
history).

---

## `labels`

`labels` is merged onto every generated object and pod template. app2kube always
seeds the recommended Kubernetes labels (overridable via `labels`):

| Label | Default | Notes |
|---|---|---|
| `app.kubernetes.io/name` | `name` | The (truncated) application name. |
| `app.kubernetes.io/instance` | `production` | Set to the staging name (or `staging-branch`) under staging. |
| `app.kubernetes.io/managed-by` | `app2kube` | Used by the prune/delete selector. |
| `app.kubernetes.io/color` | _(unset)_ | Added only when `deployment.blueGreenColor` is set. |

The Deployment / Service / PodDisruptionBudget `spec.selector` carries the full
label set (including any user labels and the color label), byte-identical to the
pod template labels, so `kubectl apply` can upgrade existing objects in place.

---

## Container spec

The values under `deployment.containers.<name>`, `deployment.initContainers.<name>`,
`cronjob.<name>.container`, and `cronjob.<name>.containers.<name>` are full native
Kubernetes [`Container`](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1/#container-v1-core)
objects — any field of the Kubernetes Container API is accepted. Commonly used
fields:

| Field | Description |
|---|---|
| `image` | Container image. Optional for app-image containers — inherited from `common.image` when omitted. See [image resolution](#image-resolution) for how it interacts with `common.image`. |
| `command` / `args` | Entry point and arguments. |
| `env` | Per-container env vars (override global `env` of the same name). |
| `ports` | Container ports. A single named port can auto-create a Service/probe. |
| `resources` | CPU/memory `requests`/`limits` (overrides `common.resources`; stripped in staging). |
| `livenessProbe` / `readinessProbe` / `startupProbe` | Health probes. |
| `lifecycle` | `postStart` / `preStop` hooks. |
| `imagePullPolicy` | Overrides `common.image.pullPolicy` and the computed default. |
| `securityContext` | Container security context (overrides the injected default; `{}` opts out). |
| `volumeMounts`, `workingDir`, … | Any other Container field. |

**What app2kube injects into app-image containers** (containers whose image comes
from `common.image`; third-party images are never modified):

- global `env` entries (without clobbering container-declared names);
- `envFrom` references to the release `ConfigMap`/`Secret`;
- `volumeMounts` for `common.sharedData` and every `volumes` entry;
- `common.resources` when the container declares none (non-staging);
- a `securityContext: { allowPrivilegeEscalation: false }` default when none is set;
- an explicit `imagePullPolicy` when none resolves.

**Auto probes** (main containers with exactly one port and no probe):

- a TCP `livenessProbe` on the single port is created when absent;
- a `readinessProbe` is **never** auto-created — an existing one only has its
  missing `httpGet`/`tcpSocket` port filled in;
- init containers never receive auto probes.

### Image resolution

How a container's `image` interacts with `common.image` (same logic for
`deployment.containers`, `deployment.initContainers`, and all cronjob
containers):

| Container `image` | Result | Treated as |
|---|---|---|
| _(omitted)_ | `common.image.repository:common.image.tag` | app image |
| same repository as `common.image.repository` (`repo`, `repo:tag`, or `repo@digest`) | repository kept, but **tag/digest replaced** with `common.image.tag` | app image |
| a different repository | left exactly as written | third-party |

The middle row is the subtle case: a per-container tag or digest on the **app
repository is discarded** in favor of `common.image.tag` — `common.image` is the
single source of the app image's tag. To deploy a different tag for one
container, point it at a different repository (third-party path) or change
`common.image.tag`. Only third-party images (different repository) keep their
exact reference and are never injected into.

---

## Defaults and hardening

app2kube fills in safe defaults when you do not set fields explicitly. All are
overridable. Summary (see [README → Defaults and hardening](README.md#defaults-and-hardening)
for the full rationale):

| Area | Default behavior |
|---|---|
| Liveness probe | Single-port app containers get an auto TCP liveness probe. |
| Readiness probe | Never auto-created; only a missing port on an existing probe is filled. |
| Container securityContext | `allowPrivilegeEscalation: false` (capabilities are not dropped). |
| Pod securityContext | `seccompProfile: { type: RuntimeDefault }`. |
| `automountServiceAccountToken` | `false`. |
| `enableServiceLinks` | `false`. |
| Image pull policy | Explicit `Always`/`IfNotPresent` based on tag/digest. |
| Rollout `progressDeadlineSeconds` | `900` (15 minutes). |
| `revisionHistoryLimit` | `2`. |
| PodDisruptionBudget | `minAvailable: 1`, emitted only when replicas > 1. |
| Resources | `common.resources` baseline applied to containers with none. |

---

## Staging overrides

When `staging` is set, the following values are forced (overriding user input):

```yaml
common:
  image:
    pullPolicy: Always
deployment:
  blueGreenColor: ""        # cleared
  replicaCount: 1           # or replicaCountStaging if > 0
  revisionHistoryLimit: 0
```

Additionally:

- container `resources` are stripped (denser packing);
- `common.resources` baseline is not applied;
- ingress `aliases` are suppressed;
- ingress hosts are prefixed: `staging.example.com` or `branch.staging.example.com`;
- the `app.kubernetes.io/instance` label becomes the staging (or `staging-branch`) name.

A wildcard ingress host (`*.example.com`) cannot be used with staging.

---

## Non-obvious behaviors

Behaviors that are easy to miss when writing values or generating manifests.

**Value loading.**

- A `.app2kube.yml` in the current directory is always loaded as the base, even
  when you pass your own `-f`; your files and `--set` overrides are layered on
  top. A missing file is silently skipped.
- At least one value source (`-f`, `--set`, `--set-string`, `--set-file`) is
  required, or the command fails with `values are required`.
- `--snapshot <file>` writes the merged **plaintext** values (env/configmap and
  secret values as loaded) with `0600` permissions.

**CronJobs.**

- A `container` block specified without a `command` is an **error** (it used to
  be silently dropped). Use `containers` for command-less containers, or add a
  command.
- `container` and `containers` are merged into one pod — `container` first
  (named `<name>-job`), then `containers` in sorted key order.
- A cronjob with no containers at all is an error.

**Manifest / `--type`.**

- An unknown `--type` value is an **error** (it used to be silently ignored).
  Valid values: `all`, `configmap`, `cronjob`, `deployment`, `ingress`, `pdb`,
  `pvc`, `secret`, `service`.
- Resource order in the output is fixed by the generator registry and does not
  follow the order of `--type` flags: Namespace → Secret → ConfigMap → PVC →
  CronJob → Deployment → PodDisruptionBudget → Service → Ingress TLS Secret →
  Ingress.
- TLS Secrets for ingress are emitted under `--type secret` as well, not only
  under `all`.

**Namespace.**

- A standalone `Namespace` object is emitted only with `--include-namespace`
  (and only for a non-`default` namespace). The resolved namespace is still
  stamped on every object's metadata regardless.
- The `default` namespace is stripped from object metadata so manifests stay
  portable.

**Config / secrets output.**

- `config secrets` and `config dotenv` print **decrypted** secret values to
  stdout; redirect with care. They also work without a `name` value.
- In `config dotenv`, a key present in more than one source is emitted once with
  precedence `secrets` > `env` > `configmap`.

---

## Annotated example

A representative (not exhaustive) configuration. For every available field, see
[Full reference](#full-reference-every-field) below.

```yaml
---
name: example                       # required
namespace: example-prod
labels:
  team: platform                    # merged onto every object

common:
  image:
    repository: registry.example.com/example/app
    tag: "1.4.2"                    # pin a tag for reproducible deploys
    pullSecrets: regcred
  resources:                        # baseline for containers without their own
    requests:
      cpu: 10m
      memory: 32Mi
  securityContext:
    runAsNonRoot: true
    runAsUser: 1000
  sharedData: /var/run/shared       # emptyDir shared across app containers
  podAntiAffinity: preferred

env:                                # plain env vars (app-image containers)
  LOG_LEVEL: info
configmap:                          # ConfigMap + envFrom
  APP_MODE: production
secrets:                            # Secret + envFrom (may be encrypted)
  DB_PASSWORD: "AES#...."

deployment:
  replicaCount: 3
  progressDeadlineSeconds: 600
  initContainers:
    migrate:
      command: ["/app/migrate"]
  containers:
    app:                            # inherits common.image
      ports:
        - containerPort: 8080
          name: http
      readinessProbe:
        httpGet:
          path: /healthz            # port filled in automatically
      resources:
        requests:
          cpu: 100m
          memory: 256Mi
    sidecar:
      image: prom/statsd-exporter   # third-party: not injected into

service:
  http:
    port: 80
    internalPort: 8080

ingress:
  - host: example.com
    aliases:
      - www.example.com
    letsencrypt: true
    sslRedirect: true

cronjob:
  cleanup:
    schedule: "0 0 * * *"
    concurrencyPolicy: Forbid
    container:
      command: ["/app/cleanup"]

volumes:
  data:
    mountPath: /var/lib/data
    spec:
      accessModes: [ReadWriteOnce]
      storageClassName: fast
      resources:
        requests:
          storage: 10Gi
```

---

## Full reference (every field)

Every value app2kube reads, with its default shown as the value or in a comment.
This block is a reference, not a recommended config — some fields are mutually
exclusive (e.g. `staging` clears `deployment.blueGreenColor`) or rarely set
together. Omit any field to take its default.

```yaml
---
# ── Identity ────────────────────────────────────────────────────────────────
name: example                       # REQUIRED; lowercased, "_" → "-"
namespace: ""                       # "" → resolved to `default` (flag > value > default)
staging: ""                         # set to enable a staging environment
branch: ""                          # combined with `staging` for labels/hosts

# ── Labels (merged onto every object; recommended labels are auto-seeded) ─────
labels: {}                          # e.g. {team: platform}

# ── Non-secret / secret configuration (app-image containers only) ─────────────
env: {}                             # plain env vars: {KEY: value}
configmap: {}                       # ConfigMap + envFrom: {KEY: value}
secrets: {}                         # Secret + envFrom: {KEY: "value|AES#…|RSA#…"}

# ── Shared settings ───────────────────────────────────────────────────────────
common:
  image:
    repository: ""                  # app image repo; "" → each container needs its own image
    tag: latest                     # "" is restored to "latest"
    pullPolicy: ""                  # "" → computed (Always for :latest, else IfNotPresent)
    pullSecrets: ""                 # imagePullSecrets name
  resources: null                   # baseline ResourceRequirements for containers with none
  securityContext: null             # PodSecurityContext; null → seccompProfile RuntimeDefault
  nodeSelector: {}
  tolerations: []                   # list of Toleration
  podAntiAffinity: ""               # "" | preferred | required
  dnsPolicy: ""                     # "" → ClusterFirst
  enableServiceLinks: false         # NOTE: Kubernetes default is true
  gracePeriod: 0                    # seconds; 0 → not set (k8s default 30)
  sharedData: ""                    # mount path of a shared emptyDir
  serviceAccountName: ""
  mountServiceAccountToken: false   # → automountServiceAccountToken
  cronjobSuspend: false             # force suspend on all cronjobs
  ingress:                          # defaults for every ingress[] entry
    class: ""                       # "" → falls through to "nginx"
    annotations: {}
    letsencrypt: false
    sslRedirect: false
    serviceName: ""
    servicePort: 0

# ── Deployment ────────────────────────────────────────────────────────────────
deployment:
  replicaCount: 1                   # *int32; explicit 0 = scale-to-zero
  replicaCountStaging: 0            # used instead of 1 under staging when > 0
  revisionHistoryLimit: 2           # forced to 0 under staging
  progressDeadlineSeconds: 900      # 15 min
  strategy: {}                      # DeploymentStrategy; {} → k8s RollingUpdate 25%/25%
  blueGreenColor: ""                # adds color label + name suffix; cleared under staging
  initContainers: {}                # map<name, Container> — no auto probes
  containers:                       # map<name, Container> — see "Container spec"
    app:
      image: ""                     # omit → common.image; same-repo tag is replaced by common.image.tag
      command: []
      args: []
      env: []                       # per-container env wins over global `env`
      ports: []
      resources: {}                 # overrides common.resources; stripped under staging
      livenessProbe: null           # auto TCP probe when single port & unset
      readinessProbe: null          # never auto-created; missing port filled in
      lifecycle: null
      imagePullPolicy: ""
      securityContext: null         # null → allowPrivilegeEscalation:false (app image)
      # …any other native Kubernetes Container field

# ── CronJobs (map<name, spec>) ────────────────────────────────────────────────
cronjob:
  example-job:
    schedule: "0 0 * * *"           # REQUIRED
    concurrencyPolicy: ""           # "" → Allow | Forbid | Replace
    restartPolicy: Never            # Never | OnFailure
    suspend: false                  # overridden by common.cronjobSuspend
    timeZone: ""                    # IANA TZ, e.g. America/Los_Angeles
    backoffLimit: 6
    activeDeadlineSeconds: 86400    # 1 day
    failedJobsHistoryLimit: 2
    successfulJobsHistoryLimit: 2
    container:                      # single Container (emitted when command is set)
      command: ["/app/job"]
    containers: {}                  # map<name, Container> for multiple containers

# ── Services (map<name, spec>) ────────────────────────────────────────────────
service:
  http:
    port: 0                         # fills internal+external when they are unset
    internalPort: 0                 # targetPort
    externalPort: 0                 # service port
    protocol: ""                    # "" → TCP | UDP | SCTP
    type: ""                        # "" → ClusterIP | NodePort | LoadBalancer | ExternalName

# ── Ingress (list) ────────────────────────────────────────────────────────────
ingress:
  - host: example.com              # prefixed under staging
    aliases: []                     # suppressed under staging
    path: /                         # default "/"
    class: ""                       # "" → common.ingress.class → "nginx"
    serviceName: ""                 # required when more than one service exists
    servicePort: 0                  # derived from the referenced service when 0
    letsencrypt: false
    sslRedirect: false
    annotations: {}
    tlsCrt: ""                      # inline PEM cert (with tlsKey)
    tlsKey: ""                      # inline PEM key
    tlsSecretName: ""               # "" → tls-<host>

# ── Volumes / PVCs (map<name, spec>) ──────────────────────────────────────────
volumes:
  data:
    mountPath: /var/lib/data        # REQUIRED
    spec:                           # full PersistentVolumeClaimSpec
      accessModes: [ReadWriteOnce]  # REQUIRED (empty is rejected)
      storageClassName: ""
      resources:
        requests:
          storage: 1Gi
```
