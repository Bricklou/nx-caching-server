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
    environment:
      - STORAGE_DIR=/data
      - PORT=8090
      - AUTH_TOKEN=your-secure-token-here # remove this line for no authentication
      - CLEANUP_THRESHOLD=24h
```

Start it:

```bash
docker compose up -d
```

!!! warning
    Remove or replace the example `AUTH_TOKEN` before exposing the server beyond your local network.
