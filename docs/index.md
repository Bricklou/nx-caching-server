---
icon: lucide/rocket
---

# Nx Caching Server

A lightweight, self-hosted remote cache server for [Nx](https://nx.dev) workspaces, written in Go.

It implements the Nx OpenAPI specification (v20.8+). Since Nx 20.8 you can point a workspace at your own cache server, and this one handles storage, retrieval and optional bearer-token authentication.

## Key features

- **Lightweight**: built with Go for performance and a small footprint.
- **Self-hosted**: full control over your cache infrastructure.
- **Secure**: optional bearer-token authentication, with per-repository tokens.
- **Automatic cleanup**: configurable cache expiration.
- **Docker ready**: pre-built images available.
- **Nx compatible**: follows the Nx OpenAPI specification v20.8+.

## Where to go next

<div class="grid cards" markdown>

- [:lucide-rocket: **Getting started**](getting-started/index.md): run the server and connect your workspace.
- [:lucide-settings: **Configuration**](configuration.md): environment variables and defaults.
- [:lucide-container: **Deployment**](deployment/docker.md): Docker and Docker Compose.
- [:lucide-plug: **API reference**](api/index.md): endpoints and authentication.

</div>
