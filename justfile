# Hyperlight Kubernetes Device Plugin
# Run `just --list` to see all available recipes
#
# Quick Start:
#   Local (KIND):     just local-up && just plugin-build && just plugin-local-push && just plugin-local-deploy
#   Azure (ACR):      just azure-up && just plugin-build && just plugin-acr-push && just plugin-azure-deploy
#   Azure (GHCR):     just azure-up-no-acr && just plugin-azure-deploy ghcr
#
# The pattern is: plugin-build → plugin-*-push → plugin-*-deploy
#   - build:  just plugin-build
#   - push:   just plugin-local-push / just plugin-acr-push / just plugin-ghcr-push
#   - deploy: just plugin-local-deploy / just plugin-azure-deploy [acr|ghcr]

# =============================================================================
# Configuration
# =============================================================================

# Image settings
image_name := "hyperlight-device-plugin"
image_tag := env_var_or_default("IMAGE_TAG", "latest")

# Device plugin settings
device_count := env_var_or_default("DEVICE_COUNT", "2000")
device_uid := env_var_or_default("DEVICE_UID", "65534")
device_gid := env_var_or_default("DEVICE_GID", "65534")

# Local (KIND) settings
local_registry := "localhost:5000"
local_cluster := "hyperlight"

# Azure settings
resource_group := env_var_or_default("RESOURCE_GROUP", "hyperlight-rg")
cluster_name := env_var_or_default("CLUSTER_NAME", "hyperlight-cluster")
acr_name := env_var_or_default("ACR_NAME", "hyperlightacr")
location := env_var_or_default("LOCATION", "westus3")

# GHCR settings
ghcr_org := "hyperlight-dev"

# Paths
project_root := justfile_directory()
device_plugin_dir := project_root + "/device-plugin"
manifests_dir := project_root + "/deploy/manifests"

# =============================================================================
# Build recipes (registry-agnostic)
# =============================================================================

# Build device plugin binary and Docker image
plugin-build:
    #!/usr/bin/env bash
    set -euo pipefail
    cd {{device_plugin_dir}}
    echo "Building binary..."
    go mod download && go mod tidy
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o hyperlight-device-plugin .
    echo "Building image: {{image_name}}:{{image_tag}}"
    docker build -t {{image_name}}:{{image_tag}} .
    echo "✓ Build complete"

# Clean build artifacts
clean:
    cd {{device_plugin_dir}} && rm -f hyperlight-device-plugin && go clean

# =============================================================================
# Local Development (KIND)
# =============================================================================

# Create KIND cluster with local registry
local-up:
    {{project_root}}/deploy/local/setup.sh

# Tear down KIND cluster and registry
local-down:
    {{project_root}}/deploy/local/teardown.sh

# Push device plugin to local registry
plugin-local-push:
    docker tag {{image_name}}:{{image_tag}} {{local_registry}}/{{image_name}}:{{image_tag}}
    docker push {{local_registry}}/{{image_name}}:{{image_tag}}
    @echo "✓ Pushed to {{local_registry}}/{{image_name}}:{{image_tag}}"

# Undeploy device plugin from KIND cluster
plugin-local-undeploy:
    kubectl delete -f {{project_root}}/deploy/local/device-plugin.yaml --ignore-not-found

# Deploy device plugin to KIND cluster
plugin-local-deploy:
    #!/usr/bin/env bash
    set -euo pipefail
    export IMAGE="{{local_registry}}/{{image_name}}:{{image_tag}}"
    export DEVICE_COUNT="{{device_count}}"
    export DEVICE_UID="{{device_uid}}"
    export DEVICE_GID="{{device_gid}}"
    echo "Deploying with image: ${IMAGE}"
    echo "Device settings: count=${DEVICE_COUNT}, uid=${DEVICE_UID}, gid=${DEVICE_GID}"
    envsubst '${IMAGE} ${DEVICE_COUNT} ${DEVICE_UID} ${DEVICE_GID}' < {{project_root}}/deploy/local/device-plugin.yaml | kubectl apply -f -
    echo "✓ Device plugin deployed to KIND"

# =============================================================================
# Azure (AKS + ACR)
# =============================================================================

# Create Azure infrastructure (RG, ACR, AKS cluster, node pools)
azure-up:
    {{project_root}}/deploy/azure/setup.sh

# Create Azure infrastructure without ACR (for GHCR deployments)
azure-up-no-acr:
    {{project_root}}/deploy/azure/setup.sh --no-acr

# Tear down Azure infrastructure
azure-down:
    {{project_root}}/deploy/azure/teardown.sh

# Tear down AKS cluster only (keep ACR)
azure-down-cluster:
    {{project_root}}/deploy/azure/teardown.sh --cluster-only

# Get AKS cluster credentials (sets kubectl context)
get-aks-credentials:
    az aks get-credentials --resource-group {{resource_group}} --name {{cluster_name}} --overwrite-existing --file ~/.kube/config
    @echo "✓ kubectl configured for {{cluster_name}}"

# Push device plugin to ACR
plugin-acr-push:
    #!/usr/bin/env bash
    set -euo pipefail
    ACR_SERVER="{{acr_name}}.azurecr.io"
    docker tag {{image_name}}:{{image_tag}} ${ACR_SERVER}/{{image_name}}:{{image_tag}}
    # Use --expose-token for reliable authentication (recommended by Microsoft docs)
    TOKEN=$(az acr login --name {{acr_name}} --expose-token --output tsv --query accessToken)
    echo "$TOKEN" | docker login ${ACR_SERVER} --username 00000000-0000-0000-0000-000000000000 --password-stdin
    docker push ${ACR_SERVER}/{{image_name}}:{{image_tag}}
    echo "✓ Pushed to ${ACR_SERVER}/{{image_name}}:{{image_tag}}"

# Undeploy device plugin from AKS
plugin-azure-undeploy:
    kubectl delete -f {{manifests_dir}}/device-plugin.yaml --ignore-not-found

# Deploy device plugin to AKS (registry: acr or ghcr, default: acr)
plugin-azure-deploy registry="acr":
    #!/usr/bin/env bash
    set -euo pipefail
    if [ "{{registry}}" = "ghcr" ]; then
        export IMAGE="ghcr.io/{{ghcr_org}}/{{image_name}}:{{image_tag}}"
    else
        export IMAGE="{{acr_name}}.azurecr.io/{{image_name}}:{{image_tag}}"
    fi
    export DEVICE_COUNT="{{device_count}}"
    export DEVICE_UID="{{device_uid}}"
    export DEVICE_GID="{{device_gid}}"
    echo "Deploying with image: ${IMAGE}"
    echo "Device settings: count=${DEVICE_COUNT}, uid=${DEVICE_UID}, gid=${DEVICE_GID}"
    envsubst '${IMAGE} ${DEVICE_COUNT} ${DEVICE_UID} ${DEVICE_GID}' < {{manifests_dir}}/device-plugin.yaml | kubectl apply -f -
    echo "✓ Device plugin deployed to AKS"

# Stop AKS cluster (saves money!)
azure-stop:
    az aks stop -g {{resource_group}} -n {{cluster_name}} --no-wait
    @echo "✓ Cluster stopping..."

# Start AKS cluster
azure-start:
    az aks start -g {{resource_group}} -n {{cluster_name}} --no-wait
    @echo "✓ Cluster starting..."

# Destroy all Azure resources
azure-destroy:
    {{project_root}}/deploy/azure/teardown.sh --all

# =============================================================================
# GHCR (GitHub Container Registry)
# =============================================================================

# Login to GHCR (requires GITHUB_TOKEN env var)
ghcr-login:
    #!/usr/bin/env bash
    set -euo pipefail
    if [ -z "${GITHUB_TOKEN:-}" ]; then
        echo "Error: GITHUB_TOKEN environment variable required"
        echo "Create a PAT with write:packages scope at https://github.com/settings/tokens"
        exit 1
    fi
    echo "${GITHUB_TOKEN}" | docker login ghcr.io -u {{ghcr_org}} --password-stdin
    echo "✓ Logged in to GHCR"

# Push device plugin to GHCR
plugin-ghcr-push:
    #!/usr/bin/env bash
    set -euo pipefail
    GHCR_IMAGE="ghcr.io/{{ghcr_org}}/{{image_name}}:{{image_tag}}"
    docker tag {{image_name}}:{{image_tag}} ${GHCR_IMAGE}
    docker push ${GHCR_IMAGE}
    echo "✓ Pushed to ${GHCR_IMAGE}"

# Build and push device plugin to GHCR
plugin-ghcr-publish: plugin-build plugin-ghcr-push

# =============================================================================
# Status & Debugging
# =============================================================================

# Show device plugin status
status:
    @echo "=== Device Plugin Pods ==="
    kubectl get pods -n hyperlight-system -l app.kubernetes.io/name=hyperlight-device-plugin -o wide 2>/dev/null || echo "No pods found"
    @echo ""
    @echo "=== Node Resources ==="
    kubectl get nodes -o custom-columns='NAME:.metadata.name,HYPERVISOR:.metadata.labels.hyperlight\.dev/hypervisor,CAPACITY:.status.allocatable.hyperlight\.dev/hypervisor' 2>/dev/null || echo "No nodes found"

# Show device plugin logs
logs:
    kubectl logs -n hyperlight-system -l app.kubernetes.io/name=hyperlight-device-plugin --tail=50

# Follow device plugin logs
logs-follow:
    kubectl logs -n hyperlight-system -l app.kubernetes.io/name=hyperlight-device-plugin -f

# =============================================================================
# Hyperlight App (example application)
# =============================================================================

hyperlight_app_dir := project_root + "/hyperlight-app"
hyperlight_app_image := "hyperlight-hello"

# Build Hyperlight app Docker image (includes guest + host)
app-build:
    #!/usr/bin/env bash
    set -euo pipefail
    docker build -t {{hyperlight_app_image}}:{{image_tag}} {{hyperlight_app_dir}}
    echo "✓ App image built: {{hyperlight_app_image}}:{{image_tag}}"

# Push Hyperlight app to local registry
app-local-push:
    docker tag {{hyperlight_app_image}}:{{image_tag}} {{local_registry}}/{{hyperlight_app_image}}:{{image_tag}}
    docker push {{local_registry}}/{{hyperlight_app_image}}:{{image_tag}}

# Push Hyperlight app to ACR
app-acr-push:
    #!/usr/bin/env bash
    set -euo pipefail
    ACR_SERVER="{{acr_name}}.azurecr.io"
    docker tag {{hyperlight_app_image}}:{{image_tag}} ${ACR_SERVER}/{{hyperlight_app_image}}:{{image_tag}}
    docker push ${ACR_SERVER}/{{hyperlight_app_image}}:{{image_tag}}
    echo "✓ Pushed to ${ACR_SERVER}/{{hyperlight_app_image}}:{{image_tag}}"

# Push Hyperlight app to GHCR
app-ghcr-push:
    docker tag {{hyperlight_app_image}}:{{image_tag}} ghcr.io/{{ghcr_org}}/{{hyperlight_app_image}}:{{image_tag}}
    docker push ghcr.io/{{ghcr_org}}/{{hyperlight_app_image}}:{{image_tag}}
    @echo "✓ Pushed to ghcr.io/{{ghcr_org}}/{{hyperlight_app_image}}:{{image_tag}}"

# Deploy Hyperlight app to local KIND cluster (KVM only)
app-local-deploy:
    #!/usr/bin/env bash
    set -euo pipefail
    export IMAGE="{{local_registry}}/{{hyperlight_app_image}}:{{image_tag}}"
    echo "Deploying app with image: ${IMAGE}"
    envsubst '$${IMAGE}' < {{hyperlight_app_dir}}/k8s/deployment-kvm.yaml | kubectl apply -f -
    echo "✓ App deployed to KIND (KVM only)"

# Deploy Hyperlight app to AKS (registry: acr or ghcr, default: acr)
app-azure-deploy registry="acr":
    #!/usr/bin/env bash
    set -euo pipefail
    if [ "{{registry}}" = "ghcr" ]; then
        export IMAGE="ghcr.io/{{ghcr_org}}/{{hyperlight_app_image}}:{{image_tag}}"
    else
        export IMAGE="{{acr_name}}.azurecr.io/{{hyperlight_app_image}}:{{image_tag}}"
    fi
    echo "Deploying app with image: ${IMAGE}"
    envsubst '$${IMAGE}' < {{hyperlight_app_dir}}/k8s/deployment-kvm.yaml | kubectl apply -f -
    envsubst '$${IMAGE}' < {{hyperlight_app_dir}}/k8s/deployment-mshv.yaml | kubectl apply -f -
    echo "✓ App deployed to AKS (KVM + MSHV)"

# Undeploy Hyperlight app
app-undeploy:
    kubectl delete -f {{hyperlight_app_dir}}/k8s/deployment-kvm.yaml --ignore-not-found
    kubectl delete -f {{hyperlight_app_dir}}/k8s/deployment-mshv.yaml --ignore-not-found

# =============================================================================
# Development
# =============================================================================

# Format code (Go + Rust)
fmt:
    cd {{device_plugin_dir}} && go fmt ./...
    cd {{hyperlight_app_dir}} && cargo fmt --all

# Run linters (Go + Rust host only - guest is no_std)
lint:
    cd {{device_plugin_dir}} && go vet ./...
    cd {{hyperlight_app_dir}}/host && cargo clippy

# Check prerequisites
check:
    #!/usr/bin/env bash
    set -euo pipefail
    echo "Checking prerequisites..."
    
    # Platform detection
    if grep -qi microsoft /proc/version 2>/dev/null; then
        echo "✓ Running in WSL2"
    elif [[ "$OSTYPE" == "darwin"* ]]; then
        echo "⚠ macOS detected - local dev (KIND) won't work (no KVM), Azure deployment OK"
    else
        echo "✓ Running on Linux"
    fi
    
    missing=()
    
    command -v docker &>/dev/null || missing+=("docker")
    command -v kubectl &>/dev/null || missing+=("kubectl")
    command -v go &>/dev/null || missing+=("go")
    command -v envsubst &>/dev/null || missing+=("envsubst (gettext-base)")
    
    if [ ${#missing[@]} -gt 0 ]; then
        echo "❌ Missing required tools: ${missing[*]}"
        exit 1
    fi
    echo "✓ Core tools: docker, kubectl, go, envsubst"
    
    # Optional tools with version checks
    if command -v kind &>/dev/null; then
        kind_version=$(kind version | grep -oP 'v\d+\.\d+' | head -1)
        echo "✓ KIND installed (${kind_version}, requires v0.20+ for CDI support)"
    else
        echo "○ KIND not installed (needed for local dev)"
    fi
    
    # Check containerd version in KIND cluster if running
    if docker ps --format '{{"{{"}}.Names{{"}}"}}' 2>/dev/null | grep -q 'hyperlight-control-plane'; then
        cdi_enabled=$(docker exec hyperlight-control-plane grep -c 'enable_cdi = true' /etc/containerd/config.toml 2>/dev/null || echo "0")
        if [ "$cdi_enabled" -gt 0 ]; then
            echo "✓ CDI enabled in KIND containerd"
        else
            echo "⚠ CDI not enabled in KIND containerd (run 'just local-down && just local-up' to fix)"
        fi
    fi
    
    if command -v az &>/dev/null; then
        echo "✓ Azure CLI installed"
        # Check for aks-preview extension (required for MSHV node pools)
        if az extension show --name aks-preview &>/dev/null; then
            echo "✓ aks-preview extension installed"
        else
            echo "⚠ aks-preview extension not installed (required for MSHV node pools)"
            echo "  Install with: az extension add --name aks-preview"
        fi
    else
        echo "○ Azure CLI not installed (needed for Azure)"
    fi
    
    command -v rustc &>/dev/null && echo "✓ Rust installed" || echo "○ Rust not installed (optional, for building Hyperlight apps locally)"
    
    # KVM check
    if [ -e /dev/kvm ]; then
        echo "✓ /dev/kvm available"
    else
        echo "⚠ /dev/kvm not found (needed for Hyperlight)"
        if grep -qi microsoft /proc/version 2>/dev/null; then
            echo "  WSL2: Enable nested virtualization in .wslconfig and restart WSL"
            echo "  See: https://learn.microsoft.com/en-us/windows/wsl/wsl-config"
        else
            echo "  Linux: Try 'sudo modprobe kvm_intel' or 'sudo modprobe kvm_amd'"
        fi
    fi
    
    echo ""
    echo "Minimum versions: Kubernetes 1.26+, KIND 0.20+"
    echo "Run 'just install-kind' or 'just install-azure-cli' to install optional tools"

# Install KIND (requires Go)
install-kind:
    #!/usr/bin/env bash
    set -euo pipefail
    if command -v kind &>/dev/null; then
        echo "KIND is already installed: $(kind version)"
        read -p "Reinstall/update? [y/N] " -n 1 -r
        echo
        [[ ! $REPLY =~ ^[Yy]$ ]] && exit 0
    fi
    go install sigs.k8s.io/kind@latest
    echo "✓ KIND installed. Make sure $(go env GOPATH)/bin is in your PATH"

# Install Azure CLI (Linux)
install-azure-cli:
    #!/usr/bin/env bash
    set -euo pipefail
    if command -v az &>/dev/null; then
        echo "Azure CLI is already installed: $(az version -o tsv | head -1)"
        read -p "Reinstall/update? [y/N] " -n 1 -r
        echo
        [[ ! $REPLY =~ ^[Yy]$ ]] && exit 0
    fi
    curl -sL https://aka.ms/InstallAzureCLIDeb | sudo bash
    echo "✓ Azure CLI installed"

# =============================================================================
# Help
# =============================================================================

# Show current configuration
config:
    @echo "=== Image Settings ==="
    @echo "  Name:     {{image_name}}"
    @echo "  Tag:      {{image_tag}}"
    @echo ""
    @echo "=== Device Plugin Settings ==="
    @echo "  Count:    {{device_count}}  (DEVICE_COUNT)"
    @echo "  UID:      {{device_uid}}  (DEVICE_UID)"
    @echo "  GID:      {{device_gid}}  (DEVICE_GID)"
    @echo ""
    @echo "=== Local (KIND) ==="
    @echo "  Registry: {{local_registry}}"
    @echo "  Cluster:  {{local_cluster}}"
    @echo ""
    @echo "=== Azure ==="
    @echo "  RG:       {{resource_group}}"
    @echo "  Cluster:  {{cluster_name}}"
    @echo "  ACR:      {{acr_name}}.azurecr.io"
    @echo "  Location: {{location}}"
    @echo ""
    @echo "=== GHCR ==="
    @echo "  Image:    ghcr.io/{{ghcr_org}}/{{image_name}}"

# Show all recipes
help:
    @just --list
