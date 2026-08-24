# smart-hibernatable-actions-runner-controller (SHARC) 🦈

A Kubernetes Operator to autoscale bare-metal GitHub Actions runners with physical machine power management via Redfish (BMC).

## Install

1. Create Secrets for GitHub App / PAT & Redfish BMC Credentials

```yaml
# github-credentials.yaml
apiVersion: v1
kind: Secret
metadata:
  name: github-app-secret
  namespace: smart-hibernatable-actions-runner-controller
type: Opaque
stringData:
  github_app_id: "123456"
  github_app_installation_id: "12345678"
  github_app_private_key: |
    -----BEGIN RSA PRIVATE KEY-----
    ...
    -----END RSA PRIVATE KEY-----
  # Or use a Personal Access Token (PAT):
  # github_token: "ghp_xxxx"
---
# redfish-credentials.yaml
apiVersion: v1
kind: Secret
metadata:
  name: gha-machine-redfish
  namespace: smart-hibernatable-actions-runner-controller
type: Opaque
stringData:
  username: "admin"
  password: "password"
```

1. Install Operator

```shell
# Install using Helm
helm upgrade --install sharc \
  oci://ghcr.io/walnuts1018/charts/smart-hibernatable-actions-runner-controller \
  --namespace smart-hibernatable-actions-runner-controller \
  --create-namespace \
```

1. Create Custom Resources (`RunnerCluster`, `RunnerNodePool`, `RunnerMachine`, `RunnerScaleSet`)

```yaml
# sharc-resources.yaml
apiVersion: sharc.walnuts.dev/v1alpha1
kind: RunnerCluster
metadata:
  name: gha-amd64
  namespace: smart-hibernatable-actions-runner-controller
spec:
  kubeconfigSecretRef:
    name: gha-amd64-kubeconfig
    key: kubeconfig
  runnerNamespace: gha-runners
---
apiVersion: sharc.walnuts.dev/v1alpha1
kind: RunnerNodePool
metadata:
  name: amd64
  namespace: smart-hibernatable-actions-runner-controller
spec:
  clusterRef:
    name: gha-amd64
  scaling:
    minNodes: 0
    maxNodes: 2
    scaleDownDelay: 10m
  drain:
    timeout: 10m
---
apiVersion: sharc.walnuts.dev/v1alpha1
kind: RunnerMachine
metadata:
  name: gha-amd64-01
  namespace: smart-hibernatable-actions-runner-controller
spec:
  clusterRef:
    name: gha-amd64
  nodePoolRef:
    name: amd64
  nodeName: gha-amd64-01
  powerPolicy: OnDemand
  capacity:
    runnerSlots: 2
  drain:
    timeout: 10m
  redfish:
    endpoint: https://192.168.10.50
    systemID: "1"
    credentialsSecretRef:
      name: gha-machine-redfish
    tls:
      insecureSkipVerify: true
---
apiVersion: sharc.walnuts.dev/v1alpha1
kind: RunnerScaleSet
metadata:
  name: sample-runners
  namespace: smart-hibernatable-actions-runner-controller
spec:
  github:
    configURL: https://github.com/my-org
    scaleSetName: arc-sample-runners
    runnerGroup: default
    credentialsSecretRef:
      name: github-app-secret
  nodePoolRef:
    name: amd64
  scaling:
    minRunners: 0
    maxRunners: 2
  runner:
    template:
      spec:
        restartPolicy: Never
        containers:
          - name: runner
            image: ghcr.io/actions/actions-runner:latest
            resources:
              requests:
                cpu: "4"
                memory: 8Gi
```

```shell
kubectl apply -f ./sharc-resources.yaml
```

At this stage, the controller listener will connect to GitHub Actions. When jobs targeting `arc-sample-runners` are queued, SHARC powers on the physical node (`RunnerMachine`) via Redfish BMC, waits for the Kubernetes node to become ready, creates `EphemeralRunner` pods to execute the workflows, and safely drains and powers off (hibernates) idle nodes.

## OpenTelemetry Metrics

Metrics are disabled by default with `OTEL_METRICS_EXPORTER=none`. OpenTelemetry settings configured through the Helm chart are converted to standard `OTEL_*` environment variables and propagated to listener Pods.

Push metrics to an OTLP/gRPC collector:

```yaml
otel:
  metricsExporter: otlp
  serviceName: sharc
  resourceAttributes: deployment.environment.name=production
  exporter:
    endpoint: http://opentelemetry-collector.observability:4317
    protocol: grpc
```

Set `otel.exporter.protocol` to `http/protobuf` to use OTLP/HTTP. Export intervals, headers, TLS, compression, and other advanced options can be supplied through `env` using standard OpenTelemetry environment variables.

The Prometheus exporter can be enabled without adding Prometheus-specific resources to the chart:

```yaml
otel:
  metricsExporter: prometheus
env:
  - name: OTEL_EXPORTER_PROMETHEUS_HOST
    value: 0.0.0.0
  - name: OTEL_EXPORTER_PROMETHEUS_PORT
    value: "9464"
```

When using this exporter, create any required Service, ServiceMonitor, authentication, and network policy resources separately.

Environment variables unrelated to telemetry can be added directly to the manager container. Kubernetes `valueFrom` sources are also supported:

```yaml
env:
  - name: HTTP_PROXY
    value: http://proxy.example.com:3128
  - name: EXAMPLE_TOKEN
    valueFrom:
      secretKeyRef:
        name: example
        key: token
```

## Verification & Supply Chain Security

All published container images and Helm charts include cryptographically signed build provenance attestations generated via GitHub Artifact Attestations (Sigstore / in-toto SLSA v1).

You can verify the authenticity and provenance of any published artifact using the GitHub CLI:

```shell
# Verify Controller Manager Image
gh attestation verify oci://ghcr.io/walnuts1018/smart-hibernatable-actions-runner-controller/manager:<TAG> \
  --owner walnuts1018

# Verify Listener Image
gh attestation verify oci://ghcr.io/walnuts1018/smart-hibernatable-actions-runner-controller/listener:<TAG> \
  --owner walnuts1018

# Verify Runner Hook Image
gh attestation verify oci://ghcr.io/walnuts1018/smart-hibernatable-actions-runner-controller/runner-hook:<TAG> \
  --owner walnuts1018

# Verify Helm Chart OCI Artifact
gh attestation verify oci://ghcr.io/walnuts1018/charts/smart-hibernatable-actions-runner-controller:<VERSION> \
  --owner walnuts1018
```

## Development

### Prerequisites

- [mise](https://mise.jdx.dev/)
- Access to a Kubernetes cluster (or [kind](https://kind.sigs.k8s.io/))

### Install Dependencies

```shell
mise install
```

### Run Tests and Linters

```shell
# Run unit tests
mise run test

# Run linters
mise run lint
```

### Run End-to-End (E2E) Tests

```shell
# Spins up a kind cluster and runs e2e tests
mise run test-e2e
```

### Run Controller Locally

```shell
# Install CRDs into the target cluster
mise run install

# Run controller locally against current kubeconfig context
mise run run
```

### Build Binaries & Docker Images

```shell
# Build manager and listener binaries
mise run build

# Build Docker image
mise run docker-build
```
