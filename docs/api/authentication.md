---
icon: lucide/shield-check
---

# Authentication

The server authenticates CI jobs with the OIDC token that their git forge issues: GitHub Actions, GitLab CI and Forgejo Actions are supported. There is no secret to create or rotate. The job asks its forge for a short-lived token and sends it as the bearer token Nx already supports.

## How it works

1. The job requests an OIDC token from its forge, for the audience you configure.
2. Nx sends it in `Authorization: Bearer <token>`. It reads the token from `NX_SELF_HOSTED_REMOTE_CACHE_ACCESS_TOKEN`.
3. The server checks the signature against the public keys of the forge, then the issuer, the audience and the expiry, then that the repository is in your allowlist.
4. From the branch or pull request the token describes, the server decides which cache the job may write and which caches it may read (see [Scopes](#scopes)).

!!! warning "No authentication"
    When `AUTH_CONFIG` is not set, every client can read and write the cache, and the server logs a warning at startup.

## Server configuration

Set `AUTH_CONFIG` to the path of a YAML file:

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
        branch_rules:
          - match: ["release/*"]
            mode: trusted
```

The server refuses to start on an invalid file and logs every problem it found, one per line.

### Forge fields

| Field      | Required | Description                                                                                                                                         |
|------------|----------|-----------------------------------------------------------------------------------------------------------------------------------------------------|
| `name`     | yes      | Name of the forge: letters, digits, `_` and `-`, up to 64 characters, unique. It becomes a directory name.                                          |
| `type`     | yes      | `github`, `gitlab` or `forgejo`.                                                                                                                    |
| `issuer`   | yes      | Issuer URL, written exactly as in the `iss` claim of the tokens. It must use `https` and be unique across forges.                                    |
| `audience` | yes      | Audience the tokens must be issued for. Choose any value and use the same one in your CI jobs.                                                       |
| `jwks_url` | no       | URL of the key set of the forge. Without it, the server discovers it from `<issuer>/.well-known/openid-configuration`.                               |
| `repos`    | yes      | Allowlist of repositories, at least one. A token from any other repository is refused.                                                              |

The issuer and the repository id depend on the forge:

| Forge  | Issuer                                                     | Repository id                                         |
|--------|------------------------------------------------------------|-------------------------------------------------------|
| GitHub | `https://token.actions.githubusercontent.com`              | `gh api repos/OWNER/REPO --jq .id`                    |
| GitLab | the URL of your instance, for example `https://gitlab.com` | the project ID, shown on the project page             |
| Forgejo| `<instance URL>/api/actions`                               | `curl <instance URL>/api/v1/repos/OWNER/REPO` (`id`)  |

GitHub Enterprise Server uses its own issuer.

### Repository fields

| Field            | Required | Description                                                                                                                  |
|------------------|----------|------------------------------------------------------------------------------------------------------------------------------|
| `id`             | yes      | Numeric id of the repository, quoted. The id is matched, not the name, so a rename keeps working.                             |
| `name`           | no       | Label shown in logs. It is never used to decide anything.                                                                    |
| `default_branch` | yes      | Branch whose cache every build can read.                                                                                     |
| `branch_rules`   | no       | Ordered rules that say how other branches are cached, see [Branch rules](#branch-rules).                                     |

## Scopes

Each repository has several caches, called scopes. A job writes to exactly one scope and reads from a short list, so a pull request can never write what the default branch reads.

| Job                                                | Writes                       | Reads, in order                                              |
|----------------------------------------------------|------------------------------|--------------------------------------------------------------|
| Push to the default branch                         | the default scope            | the default scope                                            |
| Push to a branch matched by a `trusted` rule       | its own scope                | its own scope, the default scope                             |
| Pull request or merge request                      | its own scope                | its own scope, the target branch if trusted, the default scope |
| Push to any other branch                           | its own scope                | its own scope, the default scope                             |
| Tag, `pull_request_target` or any other event      | nothing                      | the default scope                                            |

A read-only job that tries to upload receives `403`. Entries are stored under `STORAGE_DIR/<forge>/<repository id>/<scope>/`, and each kind of scope has its own [retention](../configuration.md#cleanup-and-retention).

!!! note "GitLab merge requests"
    GitLab does not send a merge request number or its target branch, so a merge request is identified by its source branch and reads its own scope and the default scope.

### Branch rules

Rules apply to branches other than the default branch, which is always trusted. The first rule whose pattern matches wins. A branch that matches no rule uses `isolated`.

| Mode        | Effect                                                                                  |
|-------------|-----------------------------------------------------------------------------------------|
| `trusted`   | The branch has its own scope, which pull requests targeting it can read.                |
| `isolated`  | Each branch has a private scope. This is the default.                                   |
| `shared`    | All the branches of the rule share one scope.                                           |
| `per_actor` | One scope per user who pushes.                                                          |
| `read_only` | The branch reads the default scope and never writes.                                    |
| `deny`      | The token is refused with `403`.                                                        |

`match` lists glob patterns. `*` does not match `/`: `release/*` matches `release/1.0` but not `release/1.0/rc`. A `trusted` rule cannot be made of stars only, since it would make every branch trusted.

## CI setup

Set `NX_SELF_HOSTED_REMOTE_CACHE_SERVER` to the URL of the server and `NX_SELF_HOSTED_REMOTE_CACHE_ACCESS_TOKEN` to the token. The audience of the token must be the one in the configuration file.

=== "GitHub Actions"

    ```yaml
    permissions:
      id-token: write
      contents: read

    jobs:
      build:
        runs-on: ubuntu-latest
        env:
          NX_SELF_HOSTED_REMOTE_CACHE_SERVER: https://cache.example.com
        steps:
          - uses: actions/checkout@v4
          - name: Get a cache token
            run: |
              token=$(curl -sH "Authorization: bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" \
                "$ACTIONS_ID_TOKEN_REQUEST_URL&audience=nx-cache" | jq -r .value)
              echo "::add-mask::$token"
              echo "NX_SELF_HOSTED_REMOTE_CACHE_ACCESS_TOKEN=$token" >> "$GITHUB_ENV"
          - run: npx nx run-many --target=build --all
    ```

=== "GitLab CI"

    ```yaml
    build:
      variables:
        NX_SELF_HOSTED_REMOTE_CACHE_SERVER: https://cache.example.com
      id_tokens:
        NX_SELF_HOSTED_REMOTE_CACHE_ACCESS_TOKEN:
          aud: nx-cache
      script:
        - npx nx run-many --target=build --all
    ```

    The name of the `id_tokens` entry is the name of the variable, so Nx finds the token without more work.

=== "Forgejo Actions"

    ```yaml
    jobs:
      build:
        runs-on: docker
        enable-openid-connect: true
        env:
          NX_SELF_HOSTED_REMOTE_CACHE_SERVER: https://cache.example.com
        steps:
          - uses: actions/checkout@v4
          - name: Get a cache token
            run: |
              token=$(curl -sH "Authorization: bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" \
                "$ACTIONS_ID_TOKEN_REQUEST_URL&audience=nx-cache" | jq -r .value)
              echo "NX_SELF_HOSTED_REMOTE_CACHE_ACCESS_TOKEN=$token" >> "$GITHUB_ENV"
          - run: npx nx run-many --target=build --all
    ```

    Your Forgejo version must support Actions ID tokens.

!!! warning "Tokens are short-lived"
    Request the token in the job that runs Nx. A token that expires during a very long run is refused, and Nx then rebuilds the rest without the cache.

## Responses

The status codes follow the Nx specification:

| Request                                                                               | Status |
|---------------------------------------------------------------------------------------|--------|
| Upload without a valid token                                                          | `401`  |
| Download without a valid token                                                        | `403`  |
| Valid token that may not use the cache: repository not listed, branch denied          | `403`  |
| Upload with a read-only token                                                         | `403`  |

The response body is a generic message. The reason is only written to the server logs, in a `request rejected` line.

## Try it locally

From a checkout of the repository, a fake forge issues tokens without any CI job:

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

!!! danger
    `AUTH_ALLOW_INSECURE_ISSUER` is for development. The fake forge signs with a key that is public. Never set it on a server that is reachable by others.
