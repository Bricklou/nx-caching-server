# Authentication

## Single token

When `AUTH_TOKEN` is set, every request must include a bearer token in the `Authorization` header:

```http
Authorization: Bearer your-secure-token-here
```

Requests without a valid token receive `401 Unauthorized`.

## Per-repository tokens

With `AUTH_TOKENS=repoA:tokA,repoB:tokB`, several repositories can share one server. Each token reads and writes only its own `STORAGE_DIR/<name>/` directory, so one repository never sees another's cache.

- Names may contain letters, digits, `_` and `-`.
- Tokens may contain `:`.
- `AUTH_TOKENS` cannot be combined with `AUTH_TOKEN`.

!!! info "Migrating from `AUTH_TOKEN`"
    Switching to `AUTH_TOKENS` causes one cache miss. The old flat files expire through normal cleanup.
