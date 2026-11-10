# Progress: forge authentication and branch-aware cache

Last updated: 2026-11-10. Branch: `feature/oidc-auth`, everything committed. `go vet`, `gofmt` and `go test ./...` pass.
Design background: `HANDOFF-branch-aware-cache.md`.

## Where we are

CI jobs now authenticate with the OIDC token of their forge (GitHub, GitLab, Forgejo). A token becomes an `Identity` (forge, repository, write scope, read chain) and the cache handlers read and write only the scopes it grants, so a pull request cannot write the cache of the default branch. `AUTH_TOKEN` and `AUTH_TOKENS` are not read any more. Static tokens, a capture of real tokens, and the documentation remain.

| Commit | Content |
|---|---|
| `59ef4ea` | Auth config: `AUTH_CONFIG` YAML, strict parsing, validation, `AUTH_ALLOW_INSECURE_ISSUER`, branch rules, per-problem startup errors |
| `5153fb9` | Fake forge for development and tests (`oauth2-proxy/mockoidc`), `cmd/devforge`, mise tasks `forge:serve` and `forge:token` |
| `7c20972` | Scope resolver, token verifier, claim adapters for the three forges |
| `3969e8d` | Server wiring: auth middleware, route groups, cache handlers on scopes, Nx specification conformance test |
| `345e99b` | Retention per kind of scope, empty directory removal |

## Behavior

**Scopes** (`internal/scope`). `Resolve(repo, event)` returns `Access{WriteScope, ReadChain, ReadOnly}`, stored in `Identity.Access`.
- The default branch reads and writes `t-<hash>` only; trusted branches (a `trusted` rule) read their own scope then the default.
- A pull request writes `pr-<N>` (or `pr-b<hash of the source branch>` when the forge gives no number) and reads its own scope, then its base when trusted, then the default.
- Other branches follow the repository's `branch_rules` modes (`isolated` by default, `shared`, `per_actor`, `read_only`, `deny`).
- Untrusted events (tags, `pull_request_target`, unknown events) only read the default scope. `deny` answers 403.
- Branch names never appear on disk: scopes are a prefix plus 16 hex of a SHA-256.

**Verifier** (`internal/auth`). Picks the configured forge from the token's `iss`, then `coreos/go-oidc` checks signature (RS256 and ES256 only), issuer, audience and expiry; the repository must be in the forge's allowlist. Discovery is lazy and the built verifier is read without a lock; a failed discovery is remembered for 5 seconds so a dead forge does not block every request. `jwks_url` skips discovery. The clock is injected by tests only (`newVerifier`).

**Claim adapters** (`internal/auth/adapters`). `actionsAdapter` serves GitHub and Forgejo, `gitlabAdapter` serves GitLab. Pushes (`push`, `workflow_dispatch`, `schedule`, GitLab `push`, `web`, `api`, `schedule`) on a branch and same-repository pull requests are trusted to write their scope; everything else only reads. The pull request number comes from `ref` (`refs/pull/N/merge`), otherwise the source branch (`head_ref`, or GitLab's `ref`) identifies the request. In a GitLab merge request pipeline `project_id` is the source project, so a fork fails the allowlist.

**Server** (`internal/server`). `/health` is public. `/v1/cache/` is mounted behind `auth.Middleware` with a prefix strip, and a panic recovery plus a debug request log wrap every route. Without `AUTH_CONFIG` the cache is open and a warning is logged at startup. With an identity, entries live in `<STORAGE_DIR>/<forge>/<repository id>/<scope>/<hash>.cache`, every segment validated; without one they sit in `STORAGE_DIR`. GET walks the read chain (the first hit wins, only that entry's modification time is refreshed); PUT writes the write scope only and keeps 409 per scope.

**Nx specification** (`api-spec.json`, not edited). `internal/server/spec_test.go` checks the server against it. PUT without a valid token answers 401, GET answers 403 (the specification lists 401 for uploads only); a valid token that may not use the cache, or a read-only upload, answers 403; 401 and 403 carry a generic `text/plain` message that never says why. Only GET and PUT exist: HEAD and other methods answer 405 with `Allow: GET, PUT`. Bad hashes and missing `Content-Length` use 400, and 500 stays for server errors.

**Retention** (`internal/server/cache`, `internal/config/retention.go`). `CLEANUP_THRESHOLD` (default `1h`) feeds `CLEANUP_THRESHOLD_TRUSTED`, `_PR` and `_DEV` when they are unset; all must be positive. Cleanup runs at the pace of the shortest one, picks each file's retention from its scope prefix (anything outside the layout gets the default), then removes empty scope, repository and forge directories that fit the layout. Uploads recreate a directory removed in between (`createTemp`, three tries).

**Documentation and image.** `docs/` and the README describe `AUTH_CONFIG`, scopes, branch rules and the CI setup of the three forges (`docs/api/authentication.md`), the Nx status codes (`docs/api/index.md`), and the deployment with a mounted configuration file. The `Dockerfile` builds `./cmd/nx-caching-server` and `.dockerignore` keeps `.git`, `.env`, `tmp`, `docs` and build output out of the context; the image was built and run with and without `AUTH_CONFIG`.

**Development tools.** `mise run forge:serve` runs a fake forge on `127.0.0.1:9100`; `mise run forge:token -- FORGE [SCENARIO]` prints a token (`push`, `pr`, `tag`, `untrusted`) using the first forge of that type in `$AUTH_CONFIG`. The `dev` task sets `AUTH_ALLOW_INSECURE_ISSUER=true`.

## Open points and risks

- **Claim values are unverified.** The claim names match the GitHub and Forgejo discovery documents and the GitLab documentation, but the real `ref` of a pull request, the type of `repository_id` and fork behavior come from documentation only, and the fake forge produces the same shapes, so tests cannot catch a mistake there.
- **Unknown key ids** make one JWKS download per bad token with no limit, so an unauthenticated caller can make the server contact a forge.
- **JWT lifetime** against a long `nx run-many` is unmeasured; a job could see 401 or 403 midway.
- **Two GitLab merge requests from the same source branch share one scope.**
- **Default retention is `1h` everywhere**, as before. `168h` for trusted and `24h` for pull requests is a reasonable setting.
- **Operators who still set `AUTH_TOKEN`** get no error: the server runs without authentication and logs a warning. The documentation no longer mentions it.

## Remaining work

1. **Capture real tokens** from each forge (a throwaway workflow that prints the decoded claims, never the raw token), compare with the adapters and `internal/devforge/tokens.go`, and keep the payloads as fixtures.
2. **Static tokens** for developers: a read pass to the CI cache, read only by default, routed in `auth.Middleware` with `IsToken`.
3. **Check the CI snippets on real runners.** The GitHub, GitLab and Forgejo snippets of `docs/api/authentication.md` follow each forge's documentation but were not run on a real job.
4. **Rate limit or negative cache** for unknown key ids.
