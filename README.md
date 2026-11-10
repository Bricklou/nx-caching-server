# Nx Caching Server

A custom remote caching server for [Nx](https://nx.dev), written in Go.
It follows the Nx OpenAPI specification (v20.8+) and provides a lightweight, self-hosted remote cache.

Since Nx 20.8, you can use your own caching server by implementing their OpenAPI spec. This server handles storage, retrieval and authentication with the OIDC tokens of your git forge (GitHub, GitLab or Forgejo). Every repository and branch gets its own cache, so a pull request cannot write what your default branch reads.

Full documentation: <https://bricklou.github.io/nx-caching-server>

## Quick start

### From source

```bash
go build -o nx-caching-server ./cmd/nx-caching-server
./nx-caching-server
```

### With Docker

```bash
docker run -d \
  --name nx-cache \
  -p 8090:8090 \
  -v $(pwd)/nx-cache-data:/data \
  -e STORAGE_DIR=/data \
  enxtur/nx-caching-server
```

Or build your own image with `docker build -t nx-caching-server .`.

### With Docker Compose

```yaml
services:
  nx-cache:
    image: enxtur/nx-caching-server:latest
    restart: unless-stopped
    ports:
      - "8090:8090"
    volumes:
      - ./nx-cache-data:/data
    environment:
      - STORAGE_DIR=/data
```

## Configuration

The server is configured with environment variables. The main ones:

| Variable                    | Description                                                                 | Default             |
|-----------------------------|-----------------------------------------------------------------------------|---------------------|
| `PORT`                      | Port the server listens on                                                  | `8090`              |
| `STORAGE_BACKEND`           | Where entries are kept: `fs` (directory) or `s3` (S3 compatible bucket)     | `fs`                |
| `STORAGE_DIR`               | Directory of the `fs` backend                                               | System temp dir     |
| `S3_BUCKET`, `S3_ENDPOINT`, `S3_REGION`, `S3_PATH_STYLE`, `S3_PREFIX` | Bucket and connection of the `s3` backend (credentials: standard `AWS_*` variables) | none |
| `AUTH_CONFIG`               | YAML file listing the trusted forges and repositories. Unset means no auth  | unset               |
| `CLEANUP_THRESHOLD`         | Duration after which unused entries are removed                             | `1h`                |
| `CLEANUP_THRESHOLD_TRUSTED` | Same, for the default branch and trusted branches                           | `CLEANUP_THRESHOLD` |
| `CLEANUP_THRESHOLD_PR`      | Same, for pull and merge requests                                           | `CLEANUP_THRESHOLD` |
| `CLEANUP_THRESHOLD_DEV`     | Same, for the other branches                                                | `CLEANUP_THRESHOLD` |
| `LOG_LEVEL`, `LOG_FORMAT`   | Log level (`debug`, `info`, `warn`, `error`) and format (`auto`, `pretty`, `text`, `json`) | `info`, `auto` |

Durations use Go syntax: `24h` is one day, `168h` is one week. See the [configuration page](https://bricklou.github.io/nx-caching-server/configuration/) for every variable.

## Authentication

Without `AUTH_CONFIG`, every client can read and write the cache. To authenticate CI jobs, describe your forge and repositories in a file:

```yaml
forges:
  - name: github
    type: github
    issuer: https://token.actions.githubusercontent.com
    audience: nx-cache
    repos:
      - id: "123456"
        name: my-org/app
        default_branch: main
```

and start the server with `AUTH_CONFIG=/path/to/auth.yaml`. The CI job requests an OIDC token from its forge, for the audience `nx-cache`, and passes it to Nx:

```bash
export NX_SELF_HOSTED_REMOTE_CACHE_SERVER=https://cache.example.com
export NX_SELF_HOSTED_REMOTE_CACHE_ACCESS_TOKEN=<OIDC token of the job>
```

The [authentication page](https://bricklou.github.io/nx-caching-server/api/authentication/) covers the file format, the branch rules and the CI setup of GitHub Actions, GitLab CI and Forgejo Actions.

## Development

```bash
mise run dev                              # run the server, reload with `mise watch dev -r`
mise run forge:serve                      # fake git forge to try authentication
mise run forge:token -- github pr         # print a token for a situation
go test ./...
```

## Contributing

Contributions, bug reports, and feature suggestions are welcome!
Feel free to open an issue or submit a pull request.
