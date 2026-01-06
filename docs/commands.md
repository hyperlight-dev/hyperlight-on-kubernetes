# Command Reference

All commands are run using [just](https://github.com/casey/just): `just <command>`

## Quick Reference

```bash
just --list      # Show all commands
just help        # Same as above
just check       # Verify prerequisites are installed
just config      # Show current configuration
```

## Device Plugin

Build and deploy the Hyperlight device plugin that exposes `/dev/kvm` or `/dev/mshv` to pods.

| Command | Description |
|---------|-------------|
| `just plugin-build` | Build the device plugin binary and Docker image |
| `just plugin-local-push` | Push image to local KIND registry (`localhost:5000`) |
| `just plugin-local-deploy` | Deploy to KIND cluster |
| `just plugin-local-undeploy` | Remove from KIND cluster |
| `just plugin-acr-push` | Push image to Azure Container Registry |
| `just plugin-azure-deploy` | Deploy to AKS (uses ACR by default) |
| `just plugin-azure-deploy ghcr` | Deploy to AKS using GHCR image |
| `just plugin-azure-undeploy` | Remove from AKS |
| `just plugin-ghcr-push` | Push image to GitHub Container Registry |
| `just plugin-ghcr-publish` | Build and push to GHCR in one step |

## Example Application

Build and deploy the example Hyperlight "Hello World" application.

| Command | Description |
|---------|-------------|
| `just app-build` | Build the app (static musl binary, `scratch` base ~2.7MB) |
| `just app-local-push` | Push to local KIND registry |
| `just app-local-deploy` | Deploy to KIND (KVM only) |
| `just app-acr-push` | Push to Azure Container Registry |
| `just app-azure-deploy` | Deploy to AKS (both KVM and MSHV) |
| `just app-azure-deploy ghcr` | Deploy to AKS using GHCR image |
| `just app-ghcr-push` | Push to GitHub Container Registry |
| `just app-undeploy` | Remove app from cluster |

## Local Development (KIND)

Commands for running locally with KIND (Kubernetes IN Docker).

| Command | Description |
|---------|-------------|
| `just local-up` | Create KIND cluster with local registry |
| `just local-down` | Tear down KIND cluster and registry |

### Full Local Workflow

```bash
just local-up                # Create cluster
just plugin-build            # Build device plugin
just plugin-local-push       # Push to local registry
just plugin-local-deploy     # Deploy plugin
just status                  # Verify it's running

# Optional: deploy example app
just app-build
just app-local-push
just app-local-deploy
kubectl logs -l app=hyperlight-hello -f
```

## Azure (AKS)

Commands for deploying to Azure Kubernetes Service.

### Infrastructure

| Command | Description |
|---------|-------------|
| `just azure-up` | Create resource group, ACR, and AKS cluster with KVM + MSHV node pools |
| `just azure-up-no-acr` | Create AKS without ACR (use with GHCR images) |
| `just azure-down` | Delete AKS cluster and ACR (keeps resource group) |
| `just azure-down-cluster` | Delete only AKS cluster (keeps ACR) |
| `just azure-destroy` | Delete everything including resource group |
| `just azure-stop` | Stop AKS cluster (saves money when not in use) |
| `just azure-start` | Start a stopped AKS cluster |
| `just get-aks-credentials` | Configure kubectl to use the AKS cluster |

### Full Azure Workflow (ACR)

```bash
just azure-up                # Create infrastructure
just get-aks-credentials     # Configure kubectl
just plugin-build            # Build device plugin
just plugin-acr-push         # Push to ACR
just plugin-azure-deploy     # Deploy to AKS
just status                  # Verify

# Optional: deploy example app
just app-build
just app-acr-push
just app-azure-deploy
kubectl logs -l app=hyperlight-hello -f
```

### Full Azure Workflow (GHCR)

```bash
just azure-up-no-acr         # Create AKS without ACR
just get-aks-credentials     # Configure kubectl
just plugin-azure-deploy ghcr  # Deploy from public GHCR images
just status                  # Verify
```

## GitHub Container Registry

Commands for publishing to GHCR for public distribution.

| Command | Description |
|---------|-------------|
| `just ghcr-login` | Authenticate with GHCR (requires `GITHUB_TOKEN` env var) |
| `just plugin-ghcr-push` | Push device plugin image |
| `just plugin-ghcr-publish` | Build and push device plugin |
| `just app-ghcr-push` | Push example app image |

### Publishing Workflow

```bash
export GITHUB_TOKEN="ghp_xxxx"
just ghcr-login
just plugin-build
just plugin-ghcr-push
just app-build
just app-ghcr-push
```

## Monitoring

| Command | Description |
|---------|-------------|
| `just status` | Show device plugin pod status and node resources |
| `just logs` | Show device plugin logs (last 100 lines) |
| `just logs-follow` | Stream device plugin logs |

## Development

| Command | Description |
|---------|-------------|
| `just fmt` | Format Go and Rust code |
| `just lint` | Run linters (golangci-lint, clippy) |
| `just clean` | Remove build artifacts |

## Installation Helpers

| Command | Description |
|---------|-------------|
| `just install-kind` | Install KIND (requires Go) |
| `just install-azure-cli` | Install Azure CLI (Linux) |

## Configuration

Default values can be overridden with environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `RESOURCE_GROUP` | `hyperlight-rg` | Azure resource group |
| `CLUSTER_NAME` | `hyperlight-cluster` | AKS cluster name |
| `ACR_NAME` | `hyperlightacr` | Azure Container Registry name |
| `LOCATION` | `westus3` | Azure region |
| `GHCR_ORG` | `hyperlight-dev` | GitHub organisation for GHCR |
| `IMAGE_TAG` | `latest` | Docker image tag |
| `DEVICE_COUNT` | `2000` | Number of concurrent allocations per node |
| `DEVICE_UID` | `65534` | UID for device node in containers (nobody) |
| `DEVICE_GID` | `65534` | GID for device node in containers (nobody) |

Example:

```bash
export ACR_NAME="mycompanyacr"
export LOCATION="uksouth"
just azure-up
```

Example with custom device settings:

```bash
# For pods running as nobody (65534)
export DEVICE_UID="65534"
export DEVICE_GID="65534"
just plugin-local-deploy
```

View current configuration:

```bash
just config
```
