#!/bin/bash
#
# Test script for Hyperlight on Kubernetes
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

# Source common utilities
source "${PROJECT_ROOT}/deploy/common.sh"

wait_for_pod() {
    local pod_name=$1
    local timeout=${2:-120}
    local start_time=$(date +%s)
    
    log_info "Waiting for pod ${pod_name} to be ready (timeout: ${timeout}s)..."
    
    while true; do
        local current_time=$(date +%s)
        local elapsed=$((current_time - start_time))
        
        if [ $elapsed -ge $timeout ]; then
            log_error "Timeout waiting for pod ${pod_name}"
            kubectl describe pod "${pod_name}" 2>/dev/null || true
            return 1
        fi
        
        local phase=$(kubectl get pod "${pod_name}" -o jsonpath='{.status.phase}' 2>/dev/null || echo "NotFound")
        
        case "$phase" in
            Running)
                log_success "Pod ${pod_name} is running"
                return 0
                ;;
            Succeeded)
                log_success "Pod ${pod_name} completed successfully"
                return 0
                ;;
            Failed)
                log_error "Pod ${pod_name} failed"
                kubectl logs "${pod_name}" 2>/dev/null || true
                return 1
                ;;
            *)
                echo -n "."
                sleep 2
                ;;
        esac
    done
}

check_device_plugin() {
    log_info "Checking device plugin DaemonSet..."
    
    local ready=$(kubectl get daemonset -n hyperlight-system hyperlight-device-plugin -o jsonpath='{.status.numberReady}' 2>/dev/null || echo "0")
    local desired=$(kubectl get daemonset -n hyperlight-system hyperlight-device-plugin -o jsonpath='{.status.desiredNumberScheduled}' 2>/dev/null || echo "0")
    
    if [ "$ready" == "$desired" ] && [ "$ready" != "0" ]; then
        log_success "Device plugin DaemonSet ready: ${ready}/${desired}"
        return 0
    else
        log_error "Device plugin DaemonSet not ready: ${ready}/${desired}"
        kubectl get pods -n hyperlight-system -l app.kubernetes.io/name=hyperlight-device-plugin
        return 1
    fi
}

check_node_resources() {
    log_info "Checking node resources..."
    
    local nodes=$(kubectl get nodes -l hyperlight.dev/enabled=true -o name 2>/dev/null)
    
    if [ -z "$nodes" ]; then
        log_warning "No nodes with hyperlight.dev/enabled=true label found"
        return 1
    fi
    
    for node in $nodes; do
        local capacity=$(kubectl get "$node" -o jsonpath='{.status.capacity.hyperlight\.dev/hypervisor}' 2>/dev/null || echo "0")
        local allocatable=$(kubectl get "$node" -o jsonpath='{.status.allocatable.hyperlight\.dev/hypervisor}' 2>/dev/null || echo "0")
        local hypervisor=$(kubectl get "$node" -o jsonpath='{.metadata.labels.hyperlight\.dev/hypervisor}' 2>/dev/null || echo "unknown")
        
        if [ "$capacity" != "0" ]; then
            log_success "${node}: hypervisor=${hypervisor}, capacity=${capacity}, allocatable=${allocatable}"
        else
            log_warning "${node}: hyperlight.dev/hypervisor resource not found"
        fi
    done
}

test_kvm() {
    log_info "Testing KVM device injection..."
    
    # Check if KVM nodes exist
    local kvm_nodes=$(kubectl get nodes -l hyperlight.dev/hypervisor=kvm -o name 2>/dev/null | wc -l)
    if [ "$kvm_nodes" == "0" ]; then
        log_warning "No KVM nodes found, skipping KVM test"
        return 0
    fi
    
    # Clean up any existing test pod
    kubectl delete pod hyperlight-test-kvm --ignore-not-found=true 2>/dev/null
    
    # Deploy test pod
    log_info "Deploying KVM test pod..."
    kubectl apply -f "${PROJECT_ROOT}/deploy/manifests/examples/test-pod-kvm.yaml"
    
    # Wait for pod
    if ! wait_for_pod "hyperlight-test-kvm" 120; then
        log_error "KVM test failed"
        return 1
    fi
    
    # Check logs
    log_info "KVM test pod logs:"
    kubectl logs hyperlight-test-kvm
    
    # Verify device exists
    if kubectl exec hyperlight-test-kvm -- test -e /dev/kvm; then
        log_success "KVM test passed: /dev/kvm is accessible in pod"
    else
        log_error "KVM test failed: /dev/kvm not found in pod"
        return 1
    fi
}

test_mshv() {
    log_info "Testing MSHV device injection..."
    
    # Check if MSHV nodes exist
    local mshv_nodes=$(kubectl get nodes -l hyperlight.dev/hypervisor=mshv -o name 2>/dev/null | wc -l)
    if [ "$mshv_nodes" == "0" ]; then
        log_warning "No MSHV nodes found, skipping MSHV test"
        return 0
    fi
    
    # Clean up any existing test pod
    kubectl delete pod hyperlight-test-mshv --ignore-not-found=true 2>/dev/null
    
    # Deploy test pod
    log_info "Deploying MSHV test pod..."
    kubectl apply -f "${PROJECT_ROOT}/deploy/manifests/examples/test-pod-mshv.yaml"
    
    # Wait for pod
    if ! wait_for_pod "hyperlight-test-mshv" 120; then
        log_error "MSHV test failed"
        return 1
    fi
    
    # Check logs
    log_info "MSHV test pod logs:"
    kubectl logs hyperlight-test-mshv
    
    # Verify device exists
    if kubectl exec hyperlight-test-mshv -- test -e /dev/mshv; then
        log_success "MSHV test passed: /dev/mshv is accessible in pod"
    else
        log_error "MSHV test failed: /dev/mshv not found in pod"
        return 1
    fi
}

cleanup() {
    log_info "Cleaning up test pods..."
    kubectl delete pod hyperlight-test-kvm --ignore-not-found=true 2>/dev/null || true
    kubectl delete pod hyperlight-test-mshv --ignore-not-found=true 2>/dev/null || true
    log_success "Cleanup complete"
}

run_all_tests() {
    local failed=0
    
    echo "========================================"
    echo "  Hyperlight Kubernetes Tests"
    echo "========================================"
    echo ""
    
    check_device_plugin || ((failed++))
    echo ""
    
    check_node_resources || ((failed++))
    echo ""
    
    test_kvm || ((failed++))
    echo ""
    
    test_mshv || ((failed++))
    echo ""
    
    echo "========================================"
    if [ $failed -eq 0 ]; then
        log_success "All tests passed!"
    else
        log_error "${failed} test(s) failed"
        exit 1
    fi
}

usage() {
    echo "Usage: $0 [command]"
    echo ""
    echo "Commands:"
    echo "  all       Run all tests (default)"
    echo "  plugin    Check device plugin status"
    echo "  nodes     Check node resources"
    echo "  kvm       Test KVM device injection"
    echo "  mshv      Test MSHV device injection"
    echo "  cleanup   Remove test pods"
}

case "${1:-all}" in
    all)
        run_all_tests
        ;;
    plugin)
        check_device_plugin
        ;;
    nodes)
        check_node_resources
        ;;
    kvm)
        test_kvm
        ;;
    mshv)
        test_mshv
        ;;
    cleanup)
        cleanup
        ;;
    help|--help|-h)
        usage
        ;;
    *)
        log_error "Unknown command: $1"
        usage
        exit 1
        ;;
esac
