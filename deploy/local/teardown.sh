#!/bin/bash
#
# Teardown local KIND cluster and registry
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/../common.sh"

CLUSTER_NAME="${CLUSTER_NAME:-hyperlight}"
REGISTRY_NAME="${REGISTRY_NAME:-kind-registry}"

delete_cluster() {
    log_info "Deleting KIND cluster: ${CLUSTER_NAME}"
    
    if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
        kind delete cluster --name "${CLUSTER_NAME}"
        log_success "Cluster deleted"
    else
        log_info "Cluster does not exist"
    fi
}

delete_registry() {
    log_info "Deleting local registry: ${REGISTRY_NAME}"
    
    if docker inspect "${REGISTRY_NAME}" &> /dev/null; then
        docker rm -f "${REGISTRY_NAME}"
        log_success "Registry deleted"
    else
        log_info "Registry does not exist"
    fi
}

main() {
    delete_cluster
    delete_registry
    echo ""
    log_success "Local environment torn down! 👋"
}

main "$@"
