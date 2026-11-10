---
icon: lucide/link
---

# Configure your Nx workspace

Point your workspace at the caching server by setting these environment variables. You can also add them to a `.env` file or your CI configuration.

=== "Without authentication"

    ```bash
    export NX_SELF_HOSTED_REMOTE_CACHE_SERVER=http://localhost:8090
    ```

=== "With authentication (CI)"

    ```bash
    export NX_SELF_HOSTED_REMOTE_CACHE_SERVER=https://cache.example.com
    export NX_SELF_HOSTED_REMOTE_CACHE_ACCESS_TOKEN=<OIDC token of the job>
    ```

Then run your tasks as usual:

```bash
npx nx run-many --target=build --all
```

Nx reads from and writes to your self-hosted cache automatically. This speeds up builds across different machines and CI/CD pipelines.

!!! note
    With authentication enabled, the access token is not a secret you choose: it is the OIDC token that your forge issues to the job. [Authentication](../api/authentication.md) shows how to get it on GitHub, GitLab and Forgejo.
