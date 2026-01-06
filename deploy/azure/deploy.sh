#!/bin/bash
#
# Deploy Device Plugin to Azure AKS
#
# Deploys the Hyperlight device plugin to an existing AKS cluster.
# Run setup.sh first to create the infrastructure.
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

# Source common utilities
source "${SCRIPT_DIR}/../common.sh"

# Load configuration
if [ -f "${SCRIPT_DIR}/config.env" ]; then
    source "${SCRIPT_DIR}/config.env"
fi

# Configuration
RESOURCE_GROUP="${RESOURCE_GROUP:-hyperlight-rg}"
CLUSTER_NAME="${CLUSTER_NAME:-hyperlight-cluster}"
ACR_NAME="${ACR_NAME:-hyperlightacr}"
IMAGE="${IMAGE:-${ACR_NAME}.azurecr.io/hyperlight-device-plugin:latest}"

# =============================================================================
# Functions
# =============================================================================

check_cluster() {
    log_info "Checking cluster connectivity..."
    
    if ! kubectl cluster-info &> /dev/null; then
        log_error "Cannot connect to cluster. Run setup.sh first or check kubeconfig."
        exit 1
    fi
    
    local context
    context=$(kubectl config current-context)
    log_info "Connected to: ${context}"
}

deploy_device_plugin() {
    log_info "Deploying Hyperlight device plugin..."
    log_info "  Image: ${IMAGE}"
    
    local manifest="${PROJECT_ROOT}/deploy/manifests/device-plugin.yaml"
    
    if [ ! -f "${manifest}" ]; then
        log_error "Manifest not found: ${manifest}"
        exit 1
    fi
    
    # Substitute image and apply
    export IMAGE
    envsubst '${IMAGE}' < "${manifest}" | kubectl apply -f -
    
    log_success "Device plugin deployed"
}

wait_for_ready() {
    log_info "Waiting for device plugin to be ready..."
    
    local timeout=120
    local start=$(date +%s)
    
    while true; do
        local now=$(date +%s)
        local elapsed=$((now - start))
        
        if [ $elapsed -ge $timeout ]; then
            log_error "Timeout waiting for device plugin"
            kubectl get pods -n hyperlight-system
            exit 1
        fi
        
        local ready=$(kubectl get daemonset -n hyperlight-system hyperlight-device-plugin \
            -o jsonpath='{.status.numberReady}' 2>/dev/null || echo "0")
        local desired=$(kubectl get daemonset -n hyperlight-system hyperlight-device-plugin \
            -o jsonpath='{.status.desiredNumberScheduled}' 2>/dev/null || echo "0")
        
        if [ "$ready" == "$desired" ] && [ "$ready" != "0" ]; then
            log_success "Device plugin ready: ${ready}/${desired}"
            return
        fi
        
        echo -n "."
        sleep 5
    done
}

verify_resources() {
    log_info "Verifying node resources..."
    echo ""
    
    kubectl get nodes -l hyperlight.dev/enabled=true -o custom-columns=\
'NAME:.metadata.name,HYPERVISOR:.metadata.labels.hyperlight\.dev/hypervisor,CAPACITY:.status.capacity.hyperlight\.dev/hypervisor,ALLOCATABLE:.status.allocatable.hyperlight\.dev/hypervisor'
    
    echo ""
}

undeploy() {
    log_info "Removing device plugin..."
    
    local manifest="${PROJECT_ROOT}/deploy/manifests/device-plugin.yaml"
    
    if [ -f "${manifest}" ]; then
        kubectl delete -f "${manifest}" --ignore-not-found || true
    fi
    
    log_success "Device plugin removed"
}

usage() {
    echo "Usage: $0 [command]"
    echo ""
    echo "Commands:"
    echo "  deploy    Deploy device plugin (default)"
    echo "  undeploy  Remove device plugin"
    echo "  status    Show device plugin status"
    echo ""
    echo "Environment variables:"
    echo "  IMAGE     Device plugin image (default: \${ACR_NAME}.azurecr.io/hyperlight-device-plugin:latest)"
}

show_status() {
    log_info "Device plugin status:"
    kubectl get daemonset -n hyperlight-system hyperlight-device-plugin 2>/dev/null || \
        log_warning "Device plugin not deployed"
    
    echo ""
    log_info "Pods:"
    kubectl get pods -n hyperlight-system -l app=hyperlight-device-plugin 2>/dev/null || true
    
    echo ""
    verify_resources
}

# =============================================================================
# Main
# =============================================================================

case "${1:-deploy}" in
    deploy)
        check_cluster
        deploy_device_plugin
        wait_for_ready
        verify_resources
        log_success "Deployment complete!"
        ;;
    undeploy|remove)
        undeploy
        ;;
    status)
        show_status
        ;;
    -h|--help|help)
        usage
        ;;
    *)
        log_error "Unknown command: $1"
        usage
        exit 1
        ;;
esac
