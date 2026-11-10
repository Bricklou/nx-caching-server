---
icon: lucide/terminal
---

# Development

How to run the server from a checkout, try authentication without a CI job, run it on a local S3 service, and run the tests. The tasks use [mise](https://mise.jdx.dev); each one is a plain command you can also run by hand (see `mise.toml`).

## Run the server

```bash
mise run dev                  # runs the server, storage in ./tmp/storage
mise watch dev                # same, and restarts when the code changes
```

The task sets `STORAGE_DIR`, `CLEANUP_THRESHOLD` and `AUTH_ALLOW_INSECURE_ISSUER`. mise also loads a `.env` file at the root of the repository, which is where to put your own settings, for example `AUTH_CONFIG` and `LOG_LEVEL=debug`.

## Try authentication locally

A fake forge issues tokens without any CI job. It plays GitHub, GitLab or Forgejo.

```bash
mise run forge:serve                       # prints a configuration snippet
```

Save the snippet in a file and start the server with it. The fake forge uses `http`, which the server only accepts when you allow it:

```bash
AUTH_CONFIG=auth.yaml AUTH_ALLOW_INSECURE_ISSUER=true go run ./cmd/nx-caching-server
```

Then print a token for a situation and use it:

```bash
mise run forge:token -- github pr --number 7      # push, pr, tag or untrusted; github, gitlab or forgejo
curl -X PUT -H "Authorization: Bearer $TOKEN" --data-binary hello localhost:8090/v1/cache/abc
```

The token takes its issuer, audience and repository from the first forge of that type in `$AUTH_CONFIG`. Useful options: `--branch`, `--base`, `--number`, `--actor`, `--ttl` (a short lifetime shows what an expired token does) and, for GitLab, `--pipeline-id`. `mise run forge:token -- --help` lists them all.

To try a [session token](api/authentication.md#long-runs-and-session-tokens), exchange the token you printed:

```bash
curl -sX POST -H "Authorization: Bearer $TOKEN" localhost:8090/v1/auth/session
```

!!! danger
    `AUTH_ALLOW_INSECURE_ISSUER` is for development. The fake forge signs with a key that is public. Never set it on a server that is reachable by others.

## Run on a local S3 service

A [Garage](https://garagehq.deuxfleurs.fr/) node in Docker stands in for S3. It needs Docker and `docker-compose.dev.yml`:

```bash
mise run s3:up                # starts Garage, creates the bucket and a key (safe to run again)
mise run dev:s3               # runs the server on it
mise run s3:down              # stops it, add -- -v to delete its data
```

Garage listens on `127.0.0.1:3910`. The bucket is `nx-cache`, and the access key (`GK000000000000000000000001`) is fixed and public, so it is for development only. The `dev:s3` task sets the `S3_*` and `AWS_*` variables for it; to use it from your `.env`, put the same variables there.

## Run the tests

```bash
go vet ./... && go test ./...
go test -race ./...           # also catches data races
```

The tests of the S3 backend need a real S3 service, and are skipped without one. Point them at the local Garage, or any service, with these variables:

```bash
NX_TEST_S3_ENDPOINT=http://127.0.0.1:3910 \
NX_TEST_S3_REGION=garage \
AWS_ACCESS_KEY_ID=GK000000000000000000000001 \
AWS_SECRET_ACCESS_KEY=0000000000000000000000000000000000000000000000000000000000000002 \
go test ./internal/store/s3
```

`NX_TEST_S3_BUCKET` changes the bucket (`nx-cache` by default). The tests create their own prefix and clean it up. CI runs them against Garage and against SeaweedFS: Garage ignores `If-None-Match`, SeaweedFS enforces it, and a test skips itself when the service does not enforce it.

## Build the documentation

```bash
mise run docs:dev             # serves the documentation with live reload
```
