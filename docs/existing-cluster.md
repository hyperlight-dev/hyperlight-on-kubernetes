# Existing Cluster Deployment

How to deploy the Hyperlight device plugin to a Kubernetes cluster you already run, without the KIND or AKS setup scripts.

The `just local-up` and `just azure-up` scripts prepare nodes for you. On any other cluster, you need to do those steps yourself before applying the manifest.

## Prerequisites

| Requirement | Details |
|-------------|---------|
| **Nodes** | Linux `amd64` nodes exposing `/dev/kvm` or `/dev/mshv` on the host. The published image is built for `linux/amd64` only. |
| **Kubernetes** | Kubernetes 1.29+. The plugin returns CDI device references from `Allocate`, which requires the `DevicePluginCDIDevices` feature (beta and enabled by default from 1.29, GA in 1.31). |
| **Container runtime** | CDI enabled, with `/var/run/cdi` in its CDI spec directories. See [Enable CDI](#2-enable-cdi-in-the-container-runtime). |
| **Tools** | `kubectl` with cluster-admin access, and `envsubst` (`apt install gettext-base`). |

Check that a node exposes a hypervisor device:

```bash
# On the node
ls -la /dev/kvm /dev/mshv
```

## 1. Label the Hyperlight Nodes

The device plugin does **not** label nodes. Both DaemonSets in `deploy/manifests/device-plugin.yaml` only run on nodes labeled `hyperlight.dev/enabled=true`, and workloads use `hyperlight.dev/hypervisor` in their `nodeSelector` to pick a hypervisor:

```bash
# KVM nodes
kubectl label node <node-name> hyperlight.dev/enabled=true hyperlight.dev/hypervisor=kvm

# MSHV nodes
kubectl label node <node-name> hyperlight.dev/enabled=true hyperlight.dev/hypervisor=mshv
```

If the device plugin finds both devices on a node, it uses `/dev/mshv`, so label such a node `hyperlight.dev/hypervisor=mshv`.

Labels set with `kubectl label` are lost when a node is replaced. For autoscaled or managed node pools, set them in the node pool or kubelet configuration instead (for example `--node-labels`), as `deploy/azure/setup.sh` does for AKS.

## 2. Enable CDI in the Container Runtime

The device plugin writes its CDI spec to `/var/run/cdi/hyperlight.json` on each node. The container runtime must have CDI enabled and read specs from that directory, otherwise pods requesting `hyperlight.dev/hypervisor` either fail to start or start without the hypervisor device.

**containerd 2.0+** enables CDI by default and reads both `/etc/cdi` and `/var/run/cdi`. No change is needed.

**containerd 1.7** needs CDI enabled. Add these settings to the existing `[plugins."io.containerd.grpc.v1.cri"]` section of `/etc/containerd/config.toml` (create the section if it is missing):

```toml
[plugins."io.containerd.grpc.v1.cri"]
  enable_cdi = true
  cdi_spec_dirs = ["/etc/cdi", "/var/run/cdi"]
```

Then restart containerd on the node:

```bash
sudo systemctl restart containerd
```

For other runtimes, check their documentation for how to enable CDI and make sure `/var/run/cdi` is one of the spec directories.

## 3. Deploy the Device Plugin

`deploy/manifests/device-plugin.yaml` contains `${IMAGE}`, `${DEVICE_COUNT}`, `${DEVICE_UID}` and `${DEVICE_GID}` placeholders. Substitute them with `envsubst` and apply the result:

```bash
export IMAGE="ghcr.io/hyperlight-dev/hyperlight-device-plugin:latest"
export DEVICE_COUNT="2000"   # concurrent allocations per node
export DEVICE_UID="65534"    # should match your pods' runAsUser
export DEVICE_GID="65534"    # should match your pods' runAsGroup

envsubst '${IMAGE} ${DEVICE_COUNT} ${DEVICE_UID} ${DEVICE_GID}' \
  < deploy/manifests/device-plugin.yaml | kubectl apply -f -
```

Listing the variables restricts `envsubst` to these placeholders, as the `just` deploy recipes do. See [Configuration](architecture.md#configuration) for what each setting controls.

For production, replace the `latest` tag with a digest you have checked as described in [Verify Published Device Plugin Images](../README.md#verify-published-device-plugin-images), for example `ghcr.io/hyperlight-dev/hyperlight-device-plugin@sha256:<digest>`.

The manifest also requests `hyperlight.dev/device-access`, a bootstrap resource that only the device plugin should use. Reserve it for the `hyperlight-system` namespace in your admission or quota policy. See [Bootstrap device access](architecture.md#bootstrap-device-access).

## 4. Verify

Wait for both DaemonSets to become ready:

```bash
kubectl -n hyperlight-system rollout status daemonset/hyperlight-device-access
kubectl -n hyperlight-system rollout status daemonset/hyperlight-device-plugin
```

The labeled nodes should now advertise the `hyperlight.dev/hypervisor` resource:

```bash
just status
```

Or, without `just`:

```bash
kubectl get nodes -l hyperlight.dev/enabled=true -o custom-columns='NAME:.metadata.name,HYPERVISOR:.metadata.labels.hyperlight\.dev/hypervisor,ALLOCATABLE:.status.allocatable.hyperlight\.dev/hypervisor'
```

Then run a test pod as described in [Verify Device Injection](../README.md#verify-device-injection), using `test-pod-kvm.yaml` or `test-pod-mshv.yaml` to match the node's hypervisor.

## Troubleshooting

### Device plugin pods are not scheduled

The DaemonSets show `DESIRED 0` when no node has the `hyperlight.dev/enabled=true` label:

```bash
kubectl get nodes -l hyperlight.dev/enabled=true
```

### Node advertises no `hyperlight.dev/hypervisor` resource

Check the device plugin logs and readiness:

```bash
kubectl -n hyperlight-system get pods -o wide
kubectl -n hyperlight-system logs daemonset/hyperlight-device-plugin
```

The plugin reports its devices as unhealthy until the hypervisor check passes and `/var/run/cdi/hyperlight.json` is written. See [Readiness and repair](architecture.md#readiness-and-repair).

### Test pod has no hypervisor device

If the test pod starts but reports `/dev/kvm NOT FOUND` (or `/dev/mshv`), CDI is disabled in the container runtime, which then ignores the CDI device reference. If it fails to start with an error such as `unresolvable CDI devices hyperlight.dev/hypervisor=kvm`, the runtime does not read `/var/run/cdi`. In both cases, enable CDI as described in [step 2](#2-enable-cdi-in-the-container-runtime).

## Uninstall

```bash
kubectl delete -f deploy/manifests/device-plugin.yaml --ignore-not-found

# Optionally remove the node labels
kubectl label node <node-name> hyperlight.dev/enabled- hyperlight.dev/hypervisor-
```

## Next Steps

- [Architecture](architecture.md) - How the device plugin works
- [Local Development](local-development.md) - Test locally with KIND
- [Azure Deployment](azure-deployment.md) - Deploy to AKS
