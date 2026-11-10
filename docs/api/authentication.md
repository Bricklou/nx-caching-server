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

A forge token expires after a few minutes to an hour, depending on the forge. A job that may run longer first exchanges it for a longer-lived [session token](#long-runs-and-session-tokens), which Nx then sends in the same header.

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
| `api_url`  | no       | GitLab only. Base URL the server calls to find the merge request of a pipeline, the issuer when omitted. See [GitLab merge requests](#gitlab-merge-requests). |
| `repos`    | yes      | Allowlist of repositories, at least one. A token from any other repository is refused.                                                              |

The issuer and the repository id depend on the forge:

| Forge  | Issuer                                                     | Repository id                                         |
|--------|------------------------------------------------------------|-------------------------------------------------------|
| GitHub | `https://token.actions.githubusercontent.com`              | `gh api repos/OWNER/REPO --jq .id`                    |
| GitLab | the URL of your instance, for example `https://gitlab.com` | the project ID, shown on the project page             |
| Forgejo| `<ROOT_URL>/api/actions`                                   | `curl <instance URL>/api/v1/repos/OWNER/REPO` (`id`)  |

GitHub Enterprise Server uses its own issuer.

!!! note "Forgejo issuer"
    Forgejo builds the issuer from the `ROOT_URL` of the instance, not from the address the request came in on. Write it exactly as `ROOT_URL` is configured, including the scheme and any port. Tokens and keys are served by the instance without authentication, at `<issuer>/.well-known/openid-configuration` and `<issuer>/.well-known/keys`. For a local instance on `http`, set [`AUTH_ALLOW_INSECURE_ISSUER`](../configuration.md#authentication).

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

A read-only job that tries to upload receives `403`. Entries are stored under `<forge>/<repository id>/<scope>/` in the storage directory or bucket, and each kind of scope has its own [retention](../configuration.md#cleanup-and-retention).

!!! note "GitLab merge requests"
    A GitLab token names no merge request number and no target branch. By default a merge request is identified by its source branch, see [GitLab merge requests](#gitlab-merge-requests) for the exact scopes.

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

## GitLab merge requests

The ID token that GitLab gives a merge request job has no merge request number and no target branch. It only has the source branch (`ref`) and the id of the pipeline (`pipeline_id`). The server can work with that in two ways.

**Without a job token** (the default), a merge request is identified by its source branch. It writes a scope named after that branch and reads it, then the default scope. Two merge requests from the same source branch share one scope, and a merge request cannot read the scope of a trusted target branch. This is safe, since both come from the same code and the same pushers.

**With the job token**, the server asks GitLab which merge request the pipeline belongs to, and uses its number and target branch like any pull request: each merge request gets its own scope and reads the target branch scope when that branch is trusted. The job gives the server the means to ask: it adds its `CI_JOB_TOKEN` to the [session exchange](#long-runs-and-session-tokens).

```bash
curl -sX POST -H "Authorization: Bearer $CACHE_JWT" -H "Job-Token: $CI_JOB_TOKEN" \
  https://cache.example.com/v1/auth/session
```

Nothing has to be configured on the server, and no secret is stored there. The server calls the GitLab at the `issuer` of the forge, or at `api_url` when it reaches GitLab at another address.

- **What is asked.** Two read requests, authorized by the job token: the pipeline (`GET /projects/:id/pipelines/:pipeline_id`), which holds the merge request ref, and the merge request (`GET /projects/:id/merge_requests/:iid`), which holds the target branch. The pipeline comes from the signed ID token, so the job token only authorizes the calls: a job token of another job changes nothing, and a token GitLab refuses gives no scope. GitLab's answer is also checked against the ID token, the source branch must match.
- **The job token is used once, and never kept.** It is not stored, not logged and not put in the session token. It stays valid for the job only, and the server only sends it to your GitLab, over `https`. It is still a credential for your project, so the exchange must go to a server you trust, as for the cache itself.
- **Caching.** An answer is kept for one hour, a failure for 30 seconds, and concurrent exchanges of one job share one lookup. The session token carries the answer, so reading the cache afterwards does not call GitLab.
- **Only for your repositories.** The server only asks about projects on the allowlist, so tokens from any other project cost no request.
- **If GitLab cannot answer** (it is down, the job token is refused, the answer does not match), the session reads the default scope and **does not write**. It never falls back to a scope shared with other merge requests. Look for `could not find the merge request of a pipeline` in the logs.
- **Without the exchange**, or without the header, nothing is asked and the source branch scope above applies.
- **Forks.** A merge request from a fork carries the id of the fork, not yours, so it is refused as before.

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
              jwt=$(curl -sH "Authorization: bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" \
                "$ACTIONS_ID_TOKEN_REQUEST_URL&audience=nx-cache" | jq -r .value)
              token=$(curl -sX POST -H "Authorization: Bearer $jwt" \
                "$NX_SELF_HOSTED_REMOTE_CACHE_SERVER/v1/auth/session" | jq -r .token)
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
        CACHE_JWT:
          aud: nx-cache
      script:
        - |
          export NX_SELF_HOSTED_REMOTE_CACHE_ACCESS_TOKEN=$(curl -sX POST \
            -H "Authorization: Bearer $CACHE_JWT" -H "Job-Token: $CI_JOB_TOKEN" \
            "$NX_SELF_HOSTED_REMOTE_CACHE_SERVER/v1/auth/session" | jq -r .token)
        - npx nx run-many --target=build --all
    ```

    The exchange gives each merge request its own scope, see [GitLab merge requests](#gitlab-merge-requests). A GitLab token lives as long as the job, so without merge request scopes you can skip the exchange and name the `id_tokens` entry `NX_SELF_HOSTED_REMOTE_CACHE_ACCESS_TOKEN`, which Nx reads as it is.

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
              jwt=$(curl -sH "Authorization: bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" \
                "$ACTIONS_ID_TOKEN_REQUEST_URL&audience=nx-cache" | jq -r .value)
              token=$(curl -sX POST -H "Authorization: Bearer $jwt" \
                "$NX_SELF_HOSTED_REMOTE_CACHE_SERVER/v1/auth/session" | jq -r .token)
              echo "NX_SELF_HOSTED_REMOTE_CACHE_ACCESS_TOKEN=$token" >> "$GITHUB_ENV"
          - run: npx nx run-many --target=build --all
    ```

    Your Forgejo version must support Actions ID tokens. `ACTIONS_ID_TOKEN_REQUEST_URL` and `ACTIONS_ID_TOKEN_REQUEST_TOKEN` only exist in jobs that set `enable-openid-connect: true`. Without the `audience` parameter the token is issued for `<ROOT_URL>/<owner>`, which does not match the configured audience, so keep `&audience=nx-cache`.

## Long runs and session tokens

A forge token is short-lived:

| Forge   | Lifetime                                                                                     |
|---------|----------------------------------------------------------------------------------------------|
| GitHub  | About five minutes (observed, not documented)                                                |
| GitLab  | The timeout of the job, or five minutes when the job has none                                |
| Forgejo | One hour (observed on Forgejo 17, not documented)                                            |

The observed values can change with the forge version, so decode the `exp` and `iat` claims of a real token if the run time matters.

Nx reads the token once, when it starts, and cannot renew it. After the expiry the server refuses it, and **Nx stops the whole run** with `Misconfigured remote cache endpoint: Unexpected response status: 403 Forbidden`.

To avoid that, the job exchanges its forge token once, before Nx starts, for a session token that lives longer. The GitHub and Forgejo snippets above do it. On Forgejo, and on GitLab without merge request scopes, a job that always finishes before its token expires can skip the exchange. The exchange is one request:

```bash
curl -sX POST -H "Authorization: Bearer $jwt" https://cache.example.com/v1/auth/session
# {"expires_in":7200,"token":"nxs_..."}
```

The session token goes in `NX_SELF_HOSTED_REMOTE_CACHE_ACCESS_TOKEN` like any other.

- **Same rights.** The session carries the claims the server verified (repository, branch or pull request, actor), not the rights. Rights are worked out again on each request from the configuration, so a repository removed from the allowlist loses access at once.
- **Bounded life.** It expires after `SESSION_TTL` (two hours by default), and it cannot be exchanged again, so it cannot renew itself. Raise `SESSION_TTL` for jobs that run longer.
- **A forge token is needed to get one.** The exchange only accepts a valid, unexpired forge token from an allowed repository, so a token that leaked a few minutes ago cannot be turned into a session. The same rules apply as for the cache: a job that would be refused is refused here.
- **Signing key.** Sessions are signed with `SESSION_SECRET`. Without it, the server draws a random key at startup: sessions stop working when it restarts, and several instances behind one address do not accept each other's sessions. Set the same `SESSION_SECRET` on every instance in that case.

Keep the session token as secret as the forge token: whoever holds it can use the cache with the same rights until it expires. Never print it in a log.

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
