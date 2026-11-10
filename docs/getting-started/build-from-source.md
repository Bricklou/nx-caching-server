---
icon: lucide/hammer
---

# Build from source

Build and run the server from the source code. You need a recent [Go toolchain](https://go.dev/dl/).

## 1. Clone the repository

```bash
git clone https://github.com/bricklou/nx-caching-server.git
cd nx-caching-server
```

## 2. Build the binary

```bash
go build -o nx-caching-server ./cmd/nx-caching-server
```

## 3. Run it

```bash
./nx-caching-server
```

## With custom settings

```bash
STORAGE_DIR=./cache-data \
PORT=8090 \
CLEANUP_THRESHOLD=24h \
AUTH_CONFIG=./auth.yaml \
./nx-caching-server
```

`AUTH_CONFIG` enables [authentication](../api/authentication.md); leave it out to run without. See [Configuration](../configuration.md) for every available variable.
