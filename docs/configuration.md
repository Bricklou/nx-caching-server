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

Entries are kept in a directory or in an S3 compatible bucket.

| Variable          | Description                                                | Default         | Example          |
|-------------------|------------------------------------------------------------|-----------------|------------------|
| `STORAGE_BACKEND` | Where entries are kept: `fs` (directory) or `s3` (bucket)  | `fs`            | `s3`             |
| `STORAGE_DIR`     | Directory of the `fs` backend                              | System temp dir | `/data/nx-cache` |

With [authentication](#authentication) enabled, entries are stored per forge, repository and scope, as a file path with `fs` and as an object key with `s3`:

```text
<forge>/<repository id>/<scope>/<hash>.cache
```

The scope depends on the branch that wrote the entry, so a pull request never writes the cache of the default branch. Without authentication, entries have no prefix.

### S3 backend

| Variable          | Description                                                                                     | Default     | Example                 |
|-------------------|-------------------------------------------------------------------------------------------------|-------------|-------------------------|
| `S3_BUCKET`       | Bucket that holds the entries. Required with `STORAGE_BACKEND=s3`                               |             | `nx-cache`              |
| `S3_ENDPOINT`     | URL of the service. Leave unset for AWS                                                         | AWS         | `http://garage:3900`    |
| `S3_REGION`       | Region used to sign requests                                                                    | `us-east-1` | `garage`                |
| `S3_PATH_STYLE`   | Put the bucket in the URL path instead of the host name. Most self-hosted services need it      | `false`     | `true`                  |
| `S3_PREFIX`       | Prefix of every object key, to share a bucket. Letters, digits, `.`, `_`, `-` and `/` only      | none        | `nx/prod`               |

Credentials are not read from these variables: the AWS SDK finds them in its standard sources (`AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`, shared credential files, instance or pod roles).

!!! note "S3 behavior"
    The first upload of a key wins: the server checks that the object is absent and sends `If-None-Match: *`. Services that ignore that header, such as Garage, only get the check, so two simultaneous uploads of the same key can both succeed and the last one stays. Entries are removed by the server's [cleanup](#cleanup-and-retention), not by bucket lifecycle rules. A download refreshes the modification time of an entry with a copy onto itself, at most four times per shortest retention.

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
| `SESSION_TTL`                | How long a [session token](api/authentication.md#long-runs-and-session-tokens) lives, in Go duration syntax                  | `6h`                | `2h`                    |
| `SESSION_SECRET`             | Key that signs session tokens, at least 32 characters. Unset: a random key, lost on restart and not shared between instances | random              | `openssl rand -hex 32`  |

- **No authentication**: leave `AUTH_CONFIG` unset. Every client can read and write the cache, and the server logs a warning at startup.
- **Forge authentication**: set `AUTH_CONFIG`. CI jobs authenticate with the OIDC token of their forge (GitHub, GitLab or Forgejo), and each repository and branch gets its own cache scope.

Details are in [Authentication](api/authentication.md).
