# Configure your Nx workspace

Point your workspace at the caching server by setting these environment variables. You can also add them to a `.env` file or your CI configuration.

=== "Without authentication"

    ```bash
    export NX_SELF_HOSTED_REMOTE_CACHE_SERVER=http://localhost:8090
    ```

=== "With authentication"

    ```bash
    export NX_SELF_HOSTED_REMOTE_CACHE_SERVER=http://localhost:8090
    export NX_SELF_HOSTED_REMOTE_CACHE_ACCESS_TOKEN=your-secure-token-here
    ```

Then run your tasks as usual:

```bash
npx nx run-many --target=build --all
```

Nx reads from and writes to your self-hosted cache automatically. This speeds up builds across different machines and CI/CD pipelines.

!!! note
    The access token must match `AUTH_TOKEN`, or one of the tokens in `AUTH_TOKENS`. See [Authentication](../api/authentication.md).
