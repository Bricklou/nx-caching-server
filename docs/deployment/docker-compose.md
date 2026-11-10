---
icon: lucide/boxes
---

# Docker Compose

Create a `docker-compose.yml` file:

```yaml
services:
  nx-cache:
    image: enxtur/nx-caching-server:latest
    container_name: nx-caching-server
    restart: unless-stopped
    ports:
      - "8090:8090"
    volumes:
      - ./nx-cache-data:/data
      - ./auth.yaml:/etc/nx-cache/auth.yaml:ro
    environment:
      - STORAGE_DIR=/data
      - PORT=8090
      - AUTH_CONFIG=/etc/nx-cache/auth.yaml # remove this line for no authentication
      - CLEANUP_THRESHOLD=24h
```

The file `auth.yaml` lists your forges and repositories, see [Authentication](../api/authentication.md).

Start it:

```bash
docker compose up -d
```

!!! warning
    Without `AUTH_CONFIG` every client can read and write the cache. Configure authentication before exposing the server beyond your local network.
