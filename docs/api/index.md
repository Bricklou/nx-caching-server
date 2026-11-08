---
icon: lucide/plug
---

# API reference

The server implements the Nx OpenAPI specification (v20.8+). All endpoints support optional bearer-token [authentication](authentication.md) when `AUTH_TOKEN` or `AUTH_TOKENS` is configured.

## Health check

### `GET /health`

Returns `200 OK` with the body `OK` if the server is running.

## Cache operations

All cache operations share one endpoint and differ by HTTP method.

### `PUT /v1/cache/{hash}`

Uploads a cache artifact. Requires the `Content-Length` header.

| Status         | Meaning                 |
|----------------|-------------------------|
| `200 OK`       | Uploaded                |
| `409 Conflict` | Artifact already exists |

### `HEAD /v1/cache/{hash}`

Checks whether a cache artifact exists.

| Status          | Meaning |
|-----------------|---------|
| `200 OK`        | Found   |
| `404 Not Found` | Missing |

### `GET /v1/cache/{hash}`

Downloads a cache artifact.

| Status          | Meaning                       |
|-----------------|-------------------------------|
| `200 OK`        | Returns the file content      |
| `404 Not Found` | Missing                       |
