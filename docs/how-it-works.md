---
icon: lucide/layers
---

# How the cache works

This page explains how the server stores cache entries, who can read and write which entries, when entries disappear, and what the design does and does not protect against. The other pages give the exact settings: [Configuration](configuration.md), [Authentication](api/authentication.md) and [S3 storage](deployment/s3.md).

## What Nx asks for

Nx talks to the server with two requests: `PUT /v1/cache/<hash>` to store the result of a task, and `GET /v1/cache/<hash>` to fetch it. The hash is computed by Nx from the inputs of the task, so the same hash always means the same result, whatever branch or commit produced it. The server never looks at the content of an entry.

Everything else is decided by the **token** Nx sends. The server works out from the token which repository and which branch or pull request the job belongs to, then picks the entries the job may read and the one place it may write. Nx sees a flat cache and knows nothing about this.

## Scopes

Each repository has several separate caches, called **scopes**. A job writes to exactly one scope and reads from a short list, in order. The first scope that holds the hash answers, and a miss in every scope is a `404`.

| Job                                         | Writes to                    | Reads, in order                                              |
|---------------------------------------------|------------------------------|--------------------------------------------------------------|
| Push to the default branch                  | the default branch scope     | the default branch scope                                     |
| Push to a branch with a `trusted` rule      | its own scope                | its own scope, the default branch scope                      |
| Pull request                                | its own scope                | its own scope, the target branch scope if trusted, the default branch scope |
| Push to any other branch                    | its own scope                | its own scope, the default branch scope                      |
| Tag, `pull_request_target`, unknown event   | nothing                      | the default branch scope                                     |

The rules are set per repository with [branch rules](api/authentication.md#branch-rules). A scope is named after what it holds, and never after the branch itself, so a branch name cannot escape the storage directory:

| Scope             | Holds                                        |
|-------------------|----------------------------------------------|
| `t-<16 hex>`      | A trusted branch, the default branch included |
| `pr-<number>`     | One pull request, or `pr-b<16 hex>` (its source branch) for a GitLab merge request when the job token is not sent with the [session exchange](api/authentication.md#gitlab-merge-requests) |
| `dev-<16 hex>`    | Another branch, or the shared or per-user scope of a branch rule |

Entries live at `<forge>/<repository id>/<scope>/<hash>.cache` in the storage directory, or under the same key in the bucket. The repository **id** is used, not its name, so renaming a repository keeps its cache.

## The life of an entry

1. **Write.** A job uploads a hash. The entry is published only once complete, so nobody ever reads half an upload. If the scope already holds that hash, the answer is `409`: the first writer wins, and the entry is never replaced. The same hash can exist in several scopes, each with its own copy.
2. **Read.** A job asks for a hash and gets the first hit along its read list. Reading counts as using the entry: the server refreshes its modification time, which keeps it alive. With S3 this refresh is a copy of the object onto itself, done at most four times per shortest retention.
3. **Expire.** An entry that was not used for longer than the retention of its kind of scope is removed. Cleanup runs when the server starts, then at the pace of the shortest retention.
4. **Tidy up.** Once a scope, repository or forge directory is empty it is removed too. A new upload creates it again.

The retention depends on the kind of scope, so busy shared caches and throwaway caches can live differently:

| Variable                    | Applies to                                       | Default             |
|-----------------------------|--------------------------------------------------|---------------------|
| `CLEANUP_THRESHOLD_TRUSTED` | Default branch and `trusted` branches            | `CLEANUP_THRESHOLD` |
| `CLEANUP_THRESHOLD_PR`      | Pull and merge requests                          | `CLEANUP_THRESHOLD` |
| `CLEANUP_THRESHOLD_DEV`     | Every other branch                               | `CLEANUP_THRESHOLD` |
| `CLEANUP_THRESHOLD`         | Everything else, and the fallback of the above   | `1h`                |

A common choice is a long retention for the default branch, since every build reads it, and a short one for pull requests: for example `CLEANUP_THRESHOLD_TRUSTED=168h` and `CLEANUP_THRESHOLD_PR=24h`. An entry that expired in the default branch scope is only a cache miss and the task runs again, never a wrong result.

## A pull request, step by step

1. A push to `main` builds and uploads its tasks into the `main` scope.
2. A pull request opens. Its job reads `pr-12`, then `main`. Tasks whose inputs did not change hit the `main` entries. Tasks it changed miss and run, and their results go to `pr-12`.
3. The author pushes again. Tasks that were built for the previous push hit `pr-12`.
4. Another pull request builds the same code. It cannot see `pr-12`: it reads its own scope and `main` only.
5. The pull request is merged. **Nothing is copied or promoted.** The push to `main` builds the merged code and writes its own results to the `main` scope, and `pr-12` expires once its retention has passed.

## Why scopes exist

A cache that every job can write is exposed to cache poisoning ([CVE-2025-36852, "CREEP"](https://nx.dev/blog/cve-2025-36852-critical-cache-poisoning-vulnerability-creep)). A pull request can compute the same hash as the default branch, upload a malicious result for it first, and the rule "first writer wins" then serves that result to every later build, including the release build. Checksums do not help, because the content is poisoned before it is hashed.

Scopes remove that path:

- A pull request writes only its own scope, so a poisoned entry reaches that pull request and nobody else.
- The default branch reads only its own scope, never a pull request scope, and the merge build copies nothing from a pull request.
- Whether a job is trusted comes from the signed token of its forge, never from anything the client sends. Only a push to the default branch, or to a branch you marked `trusted`, writes a scope that other jobs read.

Authentication therefore matters twice: it identifies the repository, and it is what makes the isolation trustworthy. See [Authentication](api/authentication.md).

## Without authentication

When `AUTH_CONFIG` is not set, there are no scopes: every client reads and writes one flat cache, entries are stored without a prefix, and the retention is `CLEANUP_THRESHOLD`. The server logs a warning at startup. Use this only on a network you trust, and not with pull requests from untrusted authors.

## Where entries are kept

The same model runs on two storage backends. With `fs`, entries are files in `STORAGE_DIR`. With `s3`, they are objects in a bucket, which lets several instances share the cache; see [S3 storage](deployment/s3.md). The choice changes nothing about scopes, retention or the rules above.

## Limits

- **A compromised default branch is not covered.** A malicious push to a trusted branch, or a compromised runner that builds it, can still write a trusted scope. Entries are not signed.
- **Tasks must be reproducible.** If a result depends on something Nx does not hash, such as the current time or an undeclared environment variable, a stale entry can be reused. That is a matter of the Nx configuration of the task, not of the server.
- **A branch cut from another version branch** (`v1.2.x` from `v1.1.x`) reads the default branch scope and not its parent version. There is no way to declare that inheritance yet.
- **Two GitLab merge requests from the same source branch share one scope** unless the job sends its [GitLab job token](api/authentication.md#gitlab-merge-requests) at the session exchange, since a GitLab token carries no merge request number. They come from the same code, so this is not a trust boundary.
- **A pull request from a fork has no access to the cache.** The job has no token for your repository, or its repository is not in the allowlist.
- **Expiry is by use, not by age.** An entry that is read often lives as long as it keeps being read, up to the end of the retention after its last read.
