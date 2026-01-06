#!/bin/bash
#
# Setup local KIND cluster with local registry for Hyperlight development
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/../common.sh"

CLUSTER_NAME="${CLUSTER_NAME:-hyperlight}"
REGISTRY_NAME="${REGISTRY_NAME:-kind-registry}"
REGISTRY_PORT="${REGISTRY_PORT:-5000}"

check_prerequisites() {
    log_info "Checking prerequisites..."
    
    require_cmd docker || exit 1
    require_cmd kind "https://kind.sigs.k8s.io or: go install sigs.k8s.io/kind@latest" || exit 1
    require_cmd kubectl || exit 1
    
    # Check for /dev/kvm
    if [ ! -e /dev/kvm ]; then
        log_warning "/dev/kvm not found - Hyperlight will not work!"
        log_info "Enable KVM: modprobe kvm_intel (or kvm_amd)"
    fi
    
    log_success "Prerequisites OK"
}

create_registry() {
    log_info "Creating local registry: ${REGISTRY_NAME}:${REGISTRY_PORT}"
    
    if docker inspect "${REGISTRY_NAME}" &> /dev/null; then
        log_warning "Registry already exists"
    else
        docker run -d --restart=always -p "127.0.0.1:${REGISTRY_PORT}:5000" \
            --network bridge --name "${REGISTRY_NAME}" registry:2
        log_success "Registry created"
    fi
}

create_cluster() {
    log_info "Creating KIND cluster: ${CLUSTER_NAME}"
    
    if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
        log_warning "Cluster already exists"
        return
    fi
    
    # Create cluster
    kind create cluster --config "${SCRIPT_DIR}/kind-config.yaml"
    
    # Connect registry to cluster network
    if [ "$(docker inspect -f='{{json .NetworkSettings.Networks.kind}}' "${REGISTRY_NAME}")" = 'null' ]; then
        docker network connect "kind" "${REGISTRY_NAME}"
    fi
    
    # Configure containerd to use local registry and enable CDI
    for node in $(kind get nodes --name "${CLUSTER_NAME}"); do
        # Local registry config
        docker exec "${node}" mkdir -p /etc/containerd/certs.d/localhost:${REGISTRY_PORT}
        cat <<EOF | docker exec -i "${node}" tee /etc/containerd/certs.d/localhost:${REGISTRY_PORT}/hosts.toml > /dev/null
[host."http://${REGISTRY_NAME}:5000"]
EOF

        # Enable CDI in containerd for device injection
        # CDI (Container Device Interface) allows the device plugin to inject
        # /dev/kvm into containers that request hyperlight.dev/hypervisor resources
        log_info "Enabling CDI on ${node}..."
        docker exec "${node}" mkdir -p /var/run/cdi
        
        # Add CDI config to containerd - enable CDI spec directories
        # containerd 1.7+ supports CDI natively
        docker exec "${node}" sed -i '/\[plugins."io.containerd.grpc.v1.cri"\]/a\    enable_cdi = true\n    cdi_spec_dirs = ["/var/run/cdi", "/etc/cdi"]' /etc/containerd/config.toml
        
        # Restart containerd to apply changes
        docker exec "${node}" systemctl restart containerd
    done
    
    # Wait for containerd to restart
    log_info "Waiting for containerd to restart..."
    sleep 5
    
    # Document the registry for kubectl
    cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: local-registry-hosting
  namespace: kube-public
data:
  localRegistryHosting.v1: |
    host: "localhost:${REGISTRY_PORT}"
    help: "https://kind.sigs.k8s.io/docs/user/local-registry/"
EOF
    
    log_success "Cluster created"
}

setup_node_labels() {
    log_info "Setting up node labels and taints..."
    
    # The labels are set in kind-config.yaml, but let's ensure they exist
    local node
    node=$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')
    
    kubectl label node "${node}" hyperlight.dev/enabled=true --overwrite
    kubectl label node "${node}" hyperlight.dev/hypervisor=kvm --overwrite
    
    # Add taint (optional for local dev, but matches production)
    kubectl taint node "${node}" hyperlight.dev/hypervisor=kvm:NoSchedule --overwrite 2>/dev/null || true
    
    log_success "Node configured"
}

print_summary() {
    echo ""
    log_success "Local Hyperlight environment ready! 🚀"
    echo ""
    echo "  Cluster:   ${CLUSTER_NAME}"
    echo "  Registry:  localhost:${REGISTRY_PORT}"
    echo "  Context:   kind-${CLUSTER_NAME}"
    echo ""
    echo "Next steps:"
    echo "  1. Build and push:  just plugin-build && just plugin-local-push"
    echo "  2. Deploy plugin:   just plugin-local-deploy"
    echo "  3. Run test pod:    kubectl apply -f deploy/manifests/examples/test-pod-kvm.yaml"
    echo ""
}

main() {
    check_prerequisites
    create_registry
    create_cluster
    setup_node_labels
    print_summary
}

main "$@"
