# Azure Deployment (AKS + ACR)

How to deploy a Hyperlight Application to Azure Kubernetes Service.

## Prerequisites

- **Azure CLI** (`az`) - [Install](https://docs.microsoft.com/en-us/cli/azure/install-azure-cli)
- **aks-preview extension** - Required for MSHV node pools
  ```bash
  az extension add --name aks-preview
  ```
- **kubectl** - Kubernetes CLI
- **Azure subscription** with permissions to create resources

## Quick Start

### Option A: With ACR (private registry)

```bash
# 1. Create Azure infrastructure (one-time)
just azure-up

# 2. Connect kubectl to the cluster
just get-aks-credentials

# 3. Build and push the device plugin to ACR
just plugin-build
just plugin-acr-push

# 4. Deploy to AKS
just plugin-azure-deploy

# 5. Verify
just status
```

### Option B: Without ACR (use GHCR)

If you don't need a private registry and want to use the public GHCR images:

```bash
# 1. Create AKS cluster only (no ACR)
just azure-up-no-acr

# 2. Connect kubectl to the cluster
just get-aks-credentials

# 3. Deploy from GHCR (no build needed assuming the plugin is already published)
just plugin-azure-deploy ghcr

# 4. Verify
just status
```

## What Gets Created

All resources are created in the same resource group (`hyperlight-rg` by default).

| Resource | Name | Description |
|----------|------|-------------|
| Resource Group | `hyperlight-rg` | Container for all resources |
| Container Registry | `hyperlightacr` | Docker images (optional, skip with `--no-acr`) |
| AKS Cluster | `hyperlight-cluster` | Kubernetes cluster |
| KVM Node Pool | `kvmpool` | Ubuntu nodes with /dev/kvm |
| MSHV Node Pool | `mshvpool` | AzureLinux nodes with /dev/mshv |

## Configuration

Override defaults with environment variables:

```bash
export RESOURCE_GROUP="my-rg"
export CLUSTER_NAME="my-cluster"
export ACR_NAME="myacr"
export LOCATION="eastus"

just azure-up
```

| Variable | Default | Description |
|----------|---------|-------------|
| `RESOURCE_GROUP` | `hyperlight-rg` | Azure resource group |
| `CLUSTER_NAME` | `hyperlight-cluster` | AKS cluster name |
| `ACR_NAME` | `hyperlightacr` | Container registry (must be globally unique) |
| `LOCATION` | `westus3` | Azure region |

## Node Pools

### KVM Pool

For workloads using Linux KVM hypervisor.

| Setting | Value |
|---------|-------|
| OS | Ubuntu |
| VM Size | Standard_D4s_v3 (nested virt capable) |
| Device | `/dev/kvm` |
| Autoscale | 1-5 nodes |

### MSHV Pool

For workloads using Microsoft Hypervisor (Azure-only).

| Setting | Value |
|---------|-------|
| OS | AzureLinux |
| Workload Runtime | KataMshvVmIsolation |
| Device | `/dev/mshv` |
| Autoscale | 1-5 nodes |

## Deployment Steps

### 1. Create Infrastructure

```bash
just azure-up
```

This runs `deploy/azure/setup.sh` which creates:
- Resource group
- ACR (attached to AKS for pull access)
- AKS cluster with system node pool
- KVM node pool
- MSHV node pool

### 2. Build and Push

```bash
# Build locally
just plugin-build

# Push to ACR
just plugin-acr-push
```

### 3. Deploy Device Plugin

```bash
just plugin-azure-deploy
```

### 4. Verify

```bash
# Check device plugin
just status

# Check node resources
kubectl get nodes -o custom-columns='NAME:.metadata.name,HYPERVISOR:.metadata.labels.hyperlight\.dev/hypervisor,CAPACITY:.status.allocatable.hyperlight\.dev/hypervisor'
```

### 5. Test Device Injection

Deploy test pods to verify the hypervisor devices are properly injected:

```bash
# Deploy test pods (KVM and MSHV)
kubectl apply -f deploy/manifests/examples/test-pod-kvm.yaml
kubectl apply -f deploy/manifests/examples/test-pod-mshv.yaml

# Check they're running
kubectl get pods -l app.kubernetes.io/name=hyperlight-test

# View logs - should show device exists
kubectl logs hyperlight-test-kvm
kubectl logs hyperlight-test-mshv

# Cleanup test pods
kubectl delete pod hyperlight-test-kvm hyperlight-test-mshv
```

Expected output:
```
=== Hyperlight KVM Test Pod ===
Checking for /dev/kvm...
✓ /dev/kvm exists
...
HYPERLIGHT_HYPERVISOR=kvm
HYPERLIGHT_DEVICE_PATH=/dev/kvm
```

## Running the Example Hyperlight App

Once the device plugin is deployed, you can run the example application. The app is built with security best practices:

- **`scratch` base image** (empty filesystem, ~2.7MB total)
- **Static musl binary** (no runtime dependencies)
- **Non-root user** (UID 65534/nobody)
- **Read-only filesystem**
- **All capabilities dropped**
- **Seccomp RuntimeDefault profile**

### Using ACR (Private)

```bash
# Build the example app
just app-build

# Push to ACR
just app-acr-push

# Deploy (creates both KVM and MSHV deployments)
just app-azure-deploy

# Check pods
kubectl get pods -l app=hyperlight-hello

# View logs from KVM pod
kubectl logs -l app=hyperlight-hello,hypervisor=kvm -f

# View logs from MSHV pod
kubectl logs -l app=hyperlight-hello,hypervisor=mshv -f
```

### Using GHCR (Public)

If the app is published to GHCR, deploy without building:

```bash
just app-azure-deploy ghcr
```

### Cleanup

```bash
just app-undeploy
```

## Resource Management

```bash
# Stop cluster when not in use (saves compute costs)
just azure-stop

# Start cluster when needed
just azure-start

# Check cluster status
az aks show -g hyperlight-rg -n hyperlight-cluster --query powerState.code

# Destroy everything when done
just azure-down
```

You can also destroy just the cluster (keeping ACR):
```bash
just azure-down-cluster
```

## Troubleshooting

### ACR name already taken

ACR names must be globally unique. Choose a different name:
```bash
export ACR_NAME="myuniquename123"
just azure-up
```

### Cluster not starting

```bash
# Check cluster status
az aks show -g hyperlight-rg -n hyperlight-cluster --query provisioningState

# View cluster events
az aks show -g hyperlight-rg -n hyperlight-cluster
```

### Node pool issues

```bash
# List node pools
az aks nodepool list -g hyperlight-rg --cluster-name hyperlight-cluster -o table

# Check specific pool
az aks nodepool show -g hyperlight-rg --cluster-name hyperlight-cluster -n kvmpool
```

### Device plugin not running

```bash
# Check pods
kubectl get pods -n hyperlight-system

# Check logs
just logs

# Describe pod
kubectl describe pod -n hyperlight-system -l app.kubernetes.io/name=hyperlight-device-plugin
```

## Next Steps

- [Local Development](local-development.md) - Test locally with KIND
- [GHCR Publishing](ghcr-publishing.md) - Publish images publicly
- [Architecture](architecture.md) - How the device plugin works
