#!/bin/bash
#
# Azure Infrastructure Setup
#
# Creates AKS cluster with KVM and MSHV node pools, and optionally ACR.
# This script handles infrastructure only - use deploy.sh to deploy apps.
#
# Usage: ./setup.sh [--no-acr]
#   --no-acr    Skip ACR creation (use when deploying from GHCR)
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/../common.sh"

# Parse arguments
SKIP_ACR=false
for arg in "$@"; do
    case $arg in
        --no-acr)
            SKIP_ACR=true
            shift
            ;;
    esac
done

# Load configuration
if [ -f "${SCRIPT_DIR}/config.env" ]; then
    source "${SCRIPT_DIR}/config.env"
fi

# =============================================================================
# Configuration (can be overridden via environment or config.env)
# =============================================================================
RESOURCE_GROUP="${RESOURCE_GROUP:-hyperlight-rg}"
CLUSTER_NAME="${CLUSTER_NAME:-hyperlight-cluster}"
LOCATION="${LOCATION:-westus3}"
SUBSCRIPTION="${SUBSCRIPTION:-}"

# Cluster configuration
SYSTEM_NODE_COUNT="${SYSTEM_NODE_COUNT:-1}"
SYSTEM_NODE_VM_SIZE="${SYSTEM_NODE_VM_SIZE:-Standard_D2s_v3}"

# KVM node pool - Ubuntu with nested virtualization
KVM_NODE_POOL_NAME="${KVM_NODE_POOL_NAME:-kvmpool}"
KVM_NODE_COUNT="${KVM_NODE_COUNT:-2}"
KVM_NODE_VM_SIZE="${KVM_NODE_VM_SIZE:-Standard_D4s_v3}"
KVM_NODE_MIN_COUNT="${KVM_NODE_MIN_COUNT:-1}"
KVM_NODE_MAX_COUNT="${KVM_NODE_MAX_COUNT:-5}"
KVM_OS_SKU="${KVM_OS_SKU:-Ubuntu}"

# MSHV node pool - AzureLinux with /dev/mshv
MSHV_NODE_POOL_NAME="${MSHV_NODE_POOL_NAME:-mshvpool}"
MSHV_NODE_COUNT="${MSHV_NODE_COUNT:-2}"
MSHV_NODE_VM_SIZE="${MSHV_NODE_VM_SIZE:-Standard_D4s_v3}"
MSHV_NODE_MIN_COUNT="${MSHV_NODE_MIN_COUNT:-1}"
MSHV_NODE_MAX_COUNT="${MSHV_NODE_MAX_COUNT:-5}"

# ACR
ACR_NAME="${ACR_NAME:-hyperlightacr}"

# =============================================================================
# Functions
# =============================================================================

check_prerequisites() {
    log_info "Checking prerequisites..."
    
    require_cmd az "https://docs.microsoft.com/en-us/cli/azure/install-azure-cli" || exit 1
    require_cmd kubectl || exit 1
    
    if ! az account show &> /dev/null; then
        log_error "Not logged in to Azure CLI. Run 'az login' first."
        exit 1
    fi
    
    log_success "Prerequisites OK"
}

set_subscription() {
    if [ -n "${SUBSCRIPTION}" ]; then
        log_info "Setting subscription: ${SUBSCRIPTION}"
        az account set --subscription "${SUBSCRIPTION}"
    fi
    log_info "Using subscription: $(az account show --query name -o tsv)"
}

create_resource_group() {
    log_info "Creating resource group: ${RESOURCE_GROUP} in ${LOCATION}"
    
    if az group show --name "${RESOURCE_GROUP}" &> /dev/null; then
        log_warning "Resource group already exists"
    else
        az group create --name "${RESOURCE_GROUP}" --location "${LOCATION}" -o none
        log_success "Resource group created"
    fi
}

create_acr() {
    log_info "Creating ACR: ${ACR_NAME}"
    
    if az acr show --name "${ACR_NAME}" &> /dev/null; then
        log_warning "ACR already exists"
    else
        az acr create \
            -g "${RESOURCE_GROUP}" \
            -n "${ACR_NAME}" \
            --sku Basic \
            -o none
        log_success "ACR created"
    fi
}

create_aks_cluster() {
    log_info "Creating AKS cluster: ${CLUSTER_NAME}"
    
    if az aks show -g "${RESOURCE_GROUP}" -n "${CLUSTER_NAME}" &> /dev/null; then
        log_warning "Cluster already exists"
    else
        # Get latest supported Kubernetes version
        local k8s_version
        k8s_version=$(az aks get-versions --location "${LOCATION}" --query "values[?isDefault].version" -o tsv | tr -d '\r')
        log_info "  Using Kubernetes version: ${k8s_version}"
        
        az aks create \
            -g "${RESOURCE_GROUP}" \
            -n "${CLUSTER_NAME}" \
            --location "${LOCATION}" \
            --kubernetes-version "${k8s_version}" \
            --node-count "${SYSTEM_NODE_COUNT}" \
            --node-vm-size "${SYSTEM_NODE_VM_SIZE}" \
            --nodepool-name "system" \
            --generate-ssh-keys \
            --enable-managed-identity \
            --network-plugin azure \
            --ssh-access disabled \
            -o none
        log_success "Cluster created"
    fi
}

create_kvm_nodepool() {
    log_info "Creating KVM node pool: ${KVM_NODE_POOL_NAME}"
    log_info "  OS: ${KVM_OS_SKU}, VM: ${KVM_NODE_VM_SIZE}"
    
    if az aks nodepool show -g "${RESOURCE_GROUP}" --cluster-name "${CLUSTER_NAME}" -n "${KVM_NODE_POOL_NAME}" &> /dev/null; then
        log_warning "KVM node pool already exists"
    else
        az aks nodepool add \
            -g "${RESOURCE_GROUP}" \
            --cluster-name "${CLUSTER_NAME}" \
            -n "${KVM_NODE_POOL_NAME}" \
            --node-count "${KVM_NODE_COUNT}" \
            --node-vm-size "${KVM_NODE_VM_SIZE}" \
            --os-sku "${KVM_OS_SKU}" \
            --enable-cluster-autoscaler \
            --min-count "${KVM_NODE_MIN_COUNT}" \
            --max-count "${KVM_NODE_MAX_COUNT}" \
            --labels "hyperlight.dev/hypervisor=kvm" "hyperlight.dev/enabled=true" \
            --mode User \
            --ssh-access disabled \
            -o none
        log_success "KVM node pool created"
    fi
}

create_mshv_nodepool() {
    log_info "Creating MSHV node pool: ${MSHV_NODE_POOL_NAME}"
    log_info "  VM: ${MSHV_NODE_VM_SIZE}, OS: AzureLinux with KataMshvVmIsolation"
    
    if az aks nodepool show -g "${RESOURCE_GROUP}" --cluster-name "${CLUSTER_NAME}" -n "${MSHV_NODE_POOL_NAME}" &> /dev/null; then
        log_warning "MSHV node pool already exists"
    else
        az aks nodepool add \
            -g "${RESOURCE_GROUP}" \
            --cluster-name "${CLUSTER_NAME}" \
            -n "${MSHV_NODE_POOL_NAME}" \
            --node-count "${MSHV_NODE_COUNT}" \
            --node-vm-size "${MSHV_NODE_VM_SIZE}" \
            --os-sku AzureLinux \
            --workload-runtime KataMshvVmIsolation \
            --enable-cluster-autoscaler \
            --min-count "${MSHV_NODE_MIN_COUNT}" \
            --max-count "${MSHV_NODE_MAX_COUNT}" \
            --labels "hyperlight.dev/hypervisor=mshv" "hyperlight.dev/enabled=true" \
            --mode User \
            --ssh-access disabled \
            -o none
        log_success "MSHV node pool created"
    fi
}

attach_acr() {
    log_info "Attaching ACR to cluster: ${ACR_NAME}"
    
    az aks update \
        -g "${RESOURCE_GROUP}" \
        -n "${CLUSTER_NAME}" \
        --attach-acr "${ACR_NAME}" \
        -o none
    
    log_success "ACR attached"
}

get_credentials() {
    log_info "Getting cluster credentials..."
    az aks get-credentials \
        -g "${RESOURCE_GROUP}" \
        -n "${CLUSTER_NAME}" \
        --overwrite-existing \
        -o none
    log_success "Credentials configured (context: ${CLUSTER_NAME})"
}

print_summary() {
    echo ""
    echo "========================================"
    echo "  Azure Infrastructure Ready"
    echo "========================================"
    echo ""
    echo "Cluster:        ${CLUSTER_NAME}"
    echo "Resource Group: ${RESOURCE_GROUP}"
    echo "Location:       ${LOCATION}"
    if [ "$SKIP_ACR" = false ]; then
        echo "ACR:            ${ACR_NAME}.azurecr.io"
    else
        echo "ACR:            (skipped - using external registry)"
    fi
    echo ""
    echo "Node Pools:"
    echo "  - ${KVM_NODE_POOL_NAME}: Ubuntu with KVM"
    echo "  - ${MSHV_NODE_POOL_NAME}: AzureLinux with MSHV"
    echo ""
    echo "Next steps:"
    if [ "$SKIP_ACR" = false ]; then
        echo "  # Connect kubectl to the cluster"
        echo "  just get-aks-credentials"
        echo ""
        echo "  # Build and push device plugin"
        echo "  just plugin-build"
        echo "  just plugin-acr-push"
        echo ""
        echo "  # Deploy to cluster"
        echo "  just plugin-azure-deploy"
    else
        echo "  # Connect kubectl to the cluster"
        echo "  just get-aks-credentials"
        echo ""
        echo "  # Deploy from GHCR (no build needed)"
        echo "  just plugin-azure-deploy ghcr"
    fi
    echo ""
}

# =============================================================================
# Main
# =============================================================================

main() {
    check_prerequisites
    set_subscription
    create_resource_group
    if [ "$SKIP_ACR" = false ]; then
        create_acr
    else
        log_info "Skipping ACR creation (--no-acr)"
    fi
    create_aks_cluster
    create_kvm_nodepool
    create_mshv_nodepool
    if [ "$SKIP_ACR" = false ]; then
        attach_acr
    fi
    get_credentials
    print_summary
}

main "$@"
