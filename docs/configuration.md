---
icon: lucide/settings
---

# Configuration

Configure the server with environment variables. All options are optional and have sensible defaults.

| Variable            | Description                                                                                                                                                                           | Default               | Example                          |
|---------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------------------|----------------------------------|
| `STORAGE_DIR`       | Directory for storing cache artifacts                                                                                                                                                 | System temp dir       | `/data/nx-cache`                 |
| `CLEANUP_THRESHOLD` | Duration after which unused cache entries are removed (hours only)                                                                                                                    | `1h`                  | `1h`, `24h`, `168h`              |
| `PORT`              | Port the server listens on                                                                                                                                                            | `8090`                | `8080`                           |
| `AUTH_TOKEN`        | Bearer token required for all requests                                                                                                                                                | unset (no auth)       | `my-secure-token`                |
| `AUTH_TOKENS`       | Comma-separated `name:token` pairs, one per repository. Each token only sees `STORAGE_DIR/<name>/`. Cannot be combined with `AUTH_TOKEN`                                              | unset                 | `repoA:tokA,repoB:tokB`          |

!!! note "Cleanup threshold"
    `CLEANUP_THRESHOLD` uses hour-based durations: `24h` is one day and `168h` is one week. The cleanup process runs automatically at that interval and removes entries that haven't been accessed within the threshold.

## Authentication modes

- **No authentication**: leave `AUTH_TOKEN` and `AUTH_TOKENS` unset.
- **Single token**: set `AUTH_TOKEN`. One shared cache for everyone.
- **Per-repository tokens**: set `AUTH_TOKENS`. Each repository gets an isolated cache.

Details are in [Authentication](api/authentication.md).
