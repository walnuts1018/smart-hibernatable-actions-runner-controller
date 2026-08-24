# smart-hibernatable-actions-runner-controller (SHARC 🦈)

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
helm install sharc \
  -n smart-hibernatable-actions-runner-controller \
  --create-namespace \
  ./charts/smart-hibernatable-actions-runner-controller
```

1. Create Custom Resources (`RunnerCluster`, `RunnerNodePool`, `RunnerMachine`, `RunnerScaleSet`)

```yaml
# sharc-resources.yaml
apiVersion: gha.walnuts.dev/v1alpha1
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
apiVersion: gha.walnuts.dev/v1alpha1
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
apiVersion: gha.walnuts.dev/v1alpha1
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
apiVersion: gha.walnuts.dev/v1alpha1
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
