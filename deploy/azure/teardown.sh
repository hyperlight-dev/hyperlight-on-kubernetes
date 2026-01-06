#!/bin/bash
#
# Azure Infrastructure Teardown
#
# Destroys the AKS cluster and optionally the entire resource group.
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/../common.sh"

# Load configuration
if [ -f "${SCRIPT_DIR}/config.env" ]; then
    source "${SCRIPT_DIR}/config.env"
fi

# Configuration
RESOURCE_GROUP="${RESOURCE_GROUP:-hyperlight-rg}"
CLUSTER_NAME="${CLUSTER_NAME:-hyperlight-cluster}"

usage() {
    echo "Usage: $0 [--cluster-only | --all]"
    echo ""
    echo "Options:"
    echo "  --cluster-only  Delete only the AKS cluster (keep resource group, ACR)"
    echo "  --all           Delete the entire resource group (default)"
    echo ""
    echo "Environment variables:"
    echo "  RESOURCE_GROUP  Resource group name (default: hyperlight-rg)"
    echo "  CLUSTER_NAME    AKS cluster name (default: hyperlight-cluster)"
}

delete_cluster_only() {
    log_warning "Deleting AKS cluster: ${CLUSTER_NAME}"
    
    if ! az aks show -g "${RESOURCE_GROUP}" -n "${CLUSTER_NAME}" &> /dev/null; then
        log_warning "Cluster not found"
        return
    fi
    
    read -p "Delete cluster ${CLUSTER_NAME}? (yes/no): " confirm
    if [ "$confirm" != "yes" ]; then
        log_info "Cancelled"
        exit 0
    fi
    
    az aks delete \
        -g "${RESOURCE_GROUP}" \
        -n "${CLUSTER_NAME}" \
        --yes \
        --no-wait
    
    log_success "Cluster deletion initiated (running in background)"
    log_info "ACR and resource group preserved"
}

delete_resource_group() {
    log_warning "Deleting resource group: ${RESOURCE_GROUP}"
    log_warning "This will delete ALL resources including:"
    log_warning "  - AKS cluster: ${CLUSTER_NAME}"
    log_warning "  - ACR and all images"
    log_warning "  - Any other resources in the group"
    
    if ! az group show --name "${RESOURCE_GROUP}" &> /dev/null; then
        log_warning "Resource group not found"
        return
    fi
    
    read -p "Delete entire resource group? (yes/no): " confirm
    if [ "$confirm" != "yes" ]; then
        log_info "Cancelled"
        exit 0
    fi
    
    az group delete \
        --name "${RESOURCE_GROUP}" \
        --yes \
        --no-wait
    
    log_success "Resource group deletion initiated (running in background)"
}

# Parse arguments
case "${1:---all}" in
    --cluster-only|cluster)
        delete_cluster_only
        ;;
    --all|all)
        delete_resource_group
        ;;
    -h|--help|help)
        usage
        ;;
    *)
        log_error "Unknown option: $1"
        usage
        exit 1
        ;;
esac
