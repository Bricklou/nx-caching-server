---
icon: lucide/plug
---

# API reference

The server implements the [Nx OpenAPI specification for self-hosted caching](https://nx.dev/docs/kb/self-hosted-caching) (v20.8+). Nx documents the endpoints, the status codes and the binary format there; this page lists how the server answers. The cache routes accept a bearer token when [authentication](authentication.md) is configured.

## Health check

### `GET /health`

Returns `200 OK` with the body `OK` if the server is running. It never requires a token.

## Cache operations

The cache operations share one endpoint and differ by HTTP method. Other methods, `HEAD` included, answer `405 Method Not Allowed`.

### `PUT /v1/cache/{hash}`

Uploads a cache artifact. Requires the `Content-Length` header. The artifact is written to the scope of the caller and never replaces an existing one.

| Status             | Meaning                                                             |
|--------------------|---------------------------------------------------------------------|
| `200 OK`           | Uploaded                                                            |
| `401 Unauthorized` | Missing or invalid token                                            |
| `403 Forbidden`    | The token may not write: read-only job, repository not allowed      |
| `409 Conflict`     | An artifact with this hash already exists in the scope of the caller |

### `GET /v1/cache/{hash}`

Downloads a cache artifact. The scopes the caller may read are searched in order and the first match is returned.

| Status          | Meaning                                                                    |
|-----------------|----------------------------------------------------------------------------|
| `200 OK`        | Returns the file content as `application/octet-stream`                     |
| `403 Forbidden` | Missing or invalid token, or the token may not use the cache               |
| `404 Not Found` | Missing in every scope the caller may read                                 |

`401` and `403` carry a short `text/plain` message, which Nx shows to the user.

!!! note "Malformed requests"
    A hash that is not made of letters, digits, `_` and `-` (128 characters at most), or an upload without `Content-Length`, answers `400 Bad Request`. A failure on the server answers `500`.
