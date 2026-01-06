# Publishing to GitHub Container Registry

Publish the Hyperlight device plugin image to GHCR for public distribution.

## Overview

Images are published to: `ghcr.io/hyperlight-dev/hyperlight-device-plugin`

Anyone can pull these images without authentication, making it easy for users to deploy the device plugin to their own clusters.

## Prerequisites

- GitHub account with access to the `hyperlight-dev` organization
- Personal Access Token (PAT) with `write:packages` scope

## Setup

### 1. Create a Personal Access Token

1. Go to [GitHub Settings > Tokens](https://github.com/settings/tokens)
2. Click "Generate new token (classic)"
3. Select scope: `write:packages`
4. Copy the token

### 2. Set Environment Variable

```bash
export GITHUB_TOKEN="ghp_xxxxxxxxxxxx"
```

Or add to your shell profile:
```bash
echo 'export GITHUB_TOKEN="ghp_xxxxxxxxxxxx"' >> ~/.bashrc
```

### 3. Login to GHCR

```bash
just ghcr-login
```

## Publishing

```bash
# Build the image
just plugin-build

# Push to GHCR
just plugin-ghcr-push
```

This pushes: `ghcr.io/hyperlight-dev/hyperlight-device-plugin:latest`

### Versioned Tags

```bash
# Push with version tag
export IMAGE_TAG="v1.0.0"
just plugin-build
just plugin-ghcr-push
```

## Using GHCR Images

Users can pull without authentication:

```yaml
# In device-plugin.yaml
image: ghcr.io/hyperlight-dev/hyperlight-device-plugin:latest
```

Or with a specific version:
```yaml
image: ghcr.io/hyperlight-dev/hyperlight-device-plugin:v1.0.0
```

## Package Visibility

By default, GHCR packages inherit repository visibility. To make public:

1. Go to the package page on GitHub
2. Click "Package settings"
3. Under "Danger Zone", click "Change visibility"
4. Select "Public"

## Troubleshooting

### "denied: permission_denied"

Your PAT needs the `write:packages` scope:
1. Go to [GitHub Settings > Tokens](https://github.com/settings/tokens)
2. Edit your token
3. Ensure `write:packages` is checked

### "unauthorized: authentication required"

Make sure you're logged in:
```bash
just ghcr-login
```

## Next Steps

- [Local Development](local-development.md) - Test locally with KIND
- [Azure Deployment](azure-deployment.md) - Deploy to AKS
- [Architecture](architecture.md) - How the device plugin works
