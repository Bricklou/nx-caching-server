---
icon: lucide/settings
---

# Configuration

Configure the server with environment variables. All options are optional and have sensible defaults.

<style>
    .md-typeset .md-typeset__table {
        display: block;
        width: 100%;
    }

    .md-typeset .md-typeset__table table:not([class]) {
        display: table;
        width: 100%;
    }

    /* Les colonnes ne passent jamais à la ligne, donc ne se réduisent pas. */
    .md-typeset .md-typeset__table th,
    .md-typeset .md-typeset__table td {
        white-space: nowrap;
    }

    /* La description prend tout l'espace restant et peut passer à la ligne. */
    .md-typeset .md-typeset__table th:nth-child(2),
    .md-typeset .md-typeset__table td:nth-child(2) {
        width: 100%;
        white-space: normal;
    }
</style>

## Server

| Variable    | Description                                                                                          | Default | Example                       |
|-------------|------------------------------------------------------------------------------------------------------|---------|-------------------------------|
| `PORT`      | Port the server listens on                                                                           | `8090`  | `8080`                        |
| `LOG_LEVEL` | Minimum level of the logs: `debug`, `info`, `warn` or `error`.<br> An invalid value falls back to `info` | `info`  | `debug`                       |
| `LOG_FORMAT`| Log format: `auto`, `pretty`, `text` or `json`.<br> `auto` colors a terminal and prints plain text otherwise | `auto`  | `json`                        |
| `NO_COLOR`  | Any non-empty value disables the colors of the `pretty` format                                       | unset   | `1`                           |

## Storage

| Variable      | Description                           | Default         | Example          |
|---------------|---------------------------------------|-----------------|------------------|
| `STORAGE_DIR` | Directory for storing cache artifacts | System temp dir | `/data/nx-cache` |

With [authentication](#authentication) enabled, entries are stored per forge, repository and scope:

```text
STORAGE_DIR/<forge>/<repository id>/<scope>/<hash>.cache
```

The scope depends on the branch that wrote the entry, so a pull request never writes the cache of the default branch. Without authentication, entries sit directly in `STORAGE_DIR`.

## Cleanup and retention

Unused cache entries are removed after a retention that depends on the kind of scope they belong to.

| Variable                    | Description                                                         | Default             | Example |
|-----------------------------|---------------------------------------------------------------------|---------------------|---------|
| `CLEANUP_THRESHOLD`         | Retention of every entry that has no more specific setting          | `1h`                | `24h`   |
| `CLEANUP_THRESHOLD_TRUSTED` | Retention of the default branch and the trusted branches            | `CLEANUP_THRESHOLD` | `168h`  |
| `CLEANUP_THRESHOLD_PR`      | Retention of pull and merge request entries                         | `CLEANUP_THRESHOLD` | `24h`   |
| `CLEANUP_THRESHOLD_DEV`     | Retention of the entries of the other branches                      | `CLEANUP_THRESHOLD` | `72h`   |

!!! note "Cleanup and retention"
    Durations use Go syntax and must be greater than zero: `24h` is one day and `168h` is one week. Cleanup runs at the pace of the shortest retention. It removes the entries that were not used within the retention of their scope, then the empty directories left behind. Downloading an entry counts as using it.

    The default branch is read by every build, so a longer `CLEANUP_THRESHOLD_TRUSTED` avoids needless misses, while pull request scopes can be kept short. Entries outside any scope (no authentication) use `CLEANUP_THRESHOLD`.

## Authentication

| Variable                     | Description                                                                                                                  | Default             | Example                 |
|------------------------------|------------------------------------------------------------------------------------------------------------------------------|---------------------|-------------------------|
| `AUTH_CONFIG`                | Path of the YAML file that lists the trusted git forges and their repositories. When unset, authentication is disabled       | unset (no auth)     | `/etc/nx-cache/auth.yaml` |
| `AUTH_ALLOW_INSECURE_ISSUER` | Accept `http` issuer and JWKS URLs. For development only: the server logs a warning at startup while it is set              | unset               | `true`                  |

- **No authentication**: leave `AUTH_CONFIG` unset. Every client can read and write the cache, and the server logs a warning at startup.
- **Forge authentication**: set `AUTH_CONFIG`. CI jobs authenticate with the OIDC token of their forge (GitHub, GitLab or Forgejo), and each repository and branch gets its own cache scope.

Details are in [Authentication](api/authentication.md).
