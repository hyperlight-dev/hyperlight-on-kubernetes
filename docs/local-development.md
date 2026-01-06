# Local Development with KIND

Quick start for testing Hyperlight on Kubernetes without cloud infrastructure.

## Prerequisites

- **Docker** - Container runtime
- **KIND** - Kubernetes IN Docker (v0.20+, includes containerd 1.7+ with CDI support)
- **kubectl** - Kubernetes CLI
- **/dev/kvm** - KVM enabled on host (required for Hyperlight)

> **Note:** The setup script automatically enables CDI (Container Device Interface) in
> KIND's containerd. CDI allows the device plugin to inject `/dev/kvm` into containers
> that request `hyperlight.dev/hypervisor` resources.

### Install KIND

```bash
# Using Go
go install sigs.k8s.io/kind@latest

# Or download binary
curl -Lo ./kind https://kind.sigs.k8s.io/docs/user/quick-start/#installation
chmod +x ./kind
sudo mv ./kind /usr/local/bin/kind
```

### Enable KVM

```bash
# Check if KVM is available
ls -la /dev/kvm
```

If not for more details on how to verify that KVM is correctly installed and permissions are correct, follow the
guide [here](https://help.ubuntu.com/community/KVM/Installation).

## Quick Start

```bash
# 1. Create KIND cluster with local registry
just local-up

# 2. Build the device plugin
just plugin-build

# 3. Push to local registry
just plugin-local-push

# 4. Deploy to KIND
just plugin-local-deploy

# 5. Verify
just status
```

## What Gets Created

| Component | Description |
|-----------|-------------|
| KIND cluster | Single-node cluster named `hyperlight` |
| Local registry | `localhost:5000` for images |
| Node labels | `hyperlight.dev/enabled=true`, `hyperlight.dev/hypervisor=kvm` |
| /dev/kvm mount | Host KVM device mounted into KIND node |

## Testing

```bash
# Check device plugin is running
just status

# Deploy a test pod
kubectl apply -f deploy/manifests/examples/test-pod-kvm.yaml

# Check the test pod
kubectl logs hyperlight-test-kvm
```
## Running the Example Hyperlight App

Once the device plugin is running, you can deploy the example Hyperlight application:

```bash
# Build the example app
just app-build

# Push to local registry
just app-local-push

# Deploy to KIND
just app-local-deploy

# Check it's running
kubectl get pods -l app=hyperlight-hello

# View logs
kubectl logs -l app=hyperlight-hello -f
```

The example app demonstrates security best practices:
- **`scratch` base image** (empty filesystem, ~2.7MB total)
- **Static musl binary** (no runtime dependencies)
- **Non-root user** (UID 65534/nobody)
- **Read-only root filesystem**
- **All capabilities dropped**
- **Seccomp RuntimeDefault profile**

### Cleanup

```bash
just app-undeploy
```

## Teardown

```bash
# Remove cluster and registry
just local-down
```

## Troubleshooting

### KIND node can't access /dev/kvm

The KIND config mounts `/dev/kvm` from the host. Ensure:
1. KVM module is loaded: `lsmod | grep kvm`
2. You have permissions: `ls -la /dev/kvm`
3. KIND node has the mount: `docker exec hyperlight-control-plane ls -la /dev/kvm`

### Image pull errors

Check the local registry is running:
```bash
docker ps | grep kind-registry
curl http://localhost:5000/v2/_catalog
```

### Device plugin not starting

```bash
# Check pod status
kubectl get pods -n hyperlight-system

# Check logs
just logs
```

## Differences from AKS Deployment

### KIND-Specific Manifest

KIND uses a modified manifest at `deploy/local/device-plugin.yaml` that differs from the AKS manifest at `deploy/manifests/device-plugin.yaml`.

**Key difference:** Sets `terminationMessagePath: /tmp/termination-log`

**Why?** KIND runs the kubelet inside a Docker container. When we mount host `/dev` to the pod's `/dev` directory, kubelet cannot create `/dev/termination-log` (the default path) inside the container, causing pod startup to fail with:

```
Error: failed to create containerd task: ... 
open .../rootfs/dev/termination-log: read-only file system
```

Moving the termination log to `/tmp` avoids this conflict while keeping the `/dev` mount for hypervisor auto-detection.

This is a KIND-specific workaround. Cloud providers like AKS run kubelet directly on the host, so they don't have this issue.

## Limitations

- **KVM only** - MSHV is assumed not to be available on local Linux hosts
- **Single node** - KIND creates a single-node cluster
- **No autoscaling** - Unlike AKS, KIND doesn't autoscale

## Next Steps

Once you've validated locally, deploy to Azure:
- [Azure Deployment Guide](azure-deployment.md)
