---
icon: lucide/container
---

# Docker

## Pre-built image

Pull the image from Docker Hub:

```bash
docker pull enxtur/nx-caching-server
```

Run it with persistent storage and authentication:

```bash
docker run -d \
  --name nx-cache \
  -p 8090:8090 \
  -v $(pwd)/nx-cache-data:/data \
  -e STORAGE_DIR=/data \
  -e AUTH_TOKEN=your-secure-token-here \
  -e CLEANUP_THRESHOLD=48h \
  enxtur/nx-caching-server
```

## Build from source

Build your own image from the repository root:

```bash
docker build -t nx-caching-server .
```

## Image details

The image uses a multi-stage build:

- **Builder stage**: a Go Alpine image compiles the application.
- **Runtime stage**: a minimal Alpine image runs the binary.

See [Docker Compose](docker-compose.md) for a ready-to-use stack.
