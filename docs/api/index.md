---
icon: lucide/plug
---

# API reference

The server implements the [Nx OpenAPI specification for self-hosted caching](https://nx.dev/docs/kb/self-hosted-caching) (v20.8+). Nx documents the endpoints, the status codes and the binary format there; this page lists how the server answers. The cache routes accept a bearer token when [authentication](authentication.md) is configured: a forge token, or a session token obtained from the [session endpoint](#session-exchange).

## Health check

### `GET /health`

Returns `200 OK` with the body `OK` if the server is running. It never requires a token.

## Session exchange

This endpoint is not part of the Nx specification. It exists only when [authentication](authentication.md) is configured, and the cache routes do not depend on it. A job that may run longer than its forge token lives calls it once, before Nx starts, to get a [session token](authentication.md#long-runs-and-session-tokens).

### `POST /v1/auth/session`

The forge token goes in the `Authorization: Bearer` header. The request has no body. A GitLab job can add its `CI_JOB_TOKEN` in a `Job-Token` header, so the server can find the merge request of the job, see [GitLab merge requests](authentication.md#gitlab-merge-requests).

```bash
curl -sX POST -H "Authorization: Bearer $FORGE_TOKEN" https://cache.example.com/v1/auth/session
```

```json
{"expires_in": 7200, "token": "nxs_..."}
```

`expires_in` is the lifetime of the session token in seconds (`SESSION_TTL`). The response is sent with `Cache-Control: no-store`. A session token carries the same repository, branch and rights as the forge token it came from, and it cannot be exchanged again.

| Status                      | Meaning                                                                                    |
|-----------------------------|--------------------------------------------------------------------------------------------|
| `200 OK`                    | Returns the session token as JSON                                                          |
| `401 Unauthorized`          | Missing, invalid or expired token, or a session token                                      |
| `403 Forbidden`             | Valid forge token that may not use the cache: repository not allowed, branch denied        |
| `405 Method Not Allowed`    | Any method other than `POST`                                                               |

`401` and `403` carry the same generic `text/plain` message as the cache routes. When authentication is not configured the route does not exist and answers `404`.

## Cache operations

The cache operations share one endpoint and differ by HTTP method. Other methods, `HEAD` included, answer `405 Method Not Allowed`.

### `PUT /v1/cache/{hash}`

Uploads a cache artifact. Requires the `Content-Length` header, which must not be negative. The artifact is written to the scope of the caller and never replaces an existing one.

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
