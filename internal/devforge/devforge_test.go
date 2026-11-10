// Tests the mockoidc-based fake forge: published keys, discovery and minted tokens.
// Tokens must verify against the key set the server publishes.
package devforge

import (
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestForgeKeyAndTokens(t *testing.T) {
	forge, err := Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = forge.Close() })

	var keys struct {
		Keys []struct {
			Kid string `json:"kid"`
		} `json:"keys"`
	}
	getJSON(t, forge.JWKSEndpoint(), &keys)
	if len(keys.Keys) != 1 || keys.Keys[0].Kid != forge.KeyID() {
		t.Fatalf("jwks = %+v, want one key with kid %q", keys, forge.KeyID())
	}

	var discovery struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}
	getJSON(t, forge.DiscoveryEndpoint(), &discovery)
	if discovery.Issuer != forge.Issuer() || discovery.JWKSURI != forge.JWKSEndpoint() {
		t.Errorf("discovery = %+v", discovery)
	}

	raw, err := forge.MintGitHub(GitHubToken{
		Audience: "nx-cache", RepoID: "1", Repo: "o/r", Ref: "refs/heads/main", EventName: "push",
	})
	if err != nil {
		t.Fatal(err)
	}

	// The token verifies against the published key and carries the issuer.
	parsed, err := forge.Keypair.VerifyJWT(raw, forge.Now)
	if err != nil {
		t.Fatalf("token does not verify: %v", err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if claims["iss"] != forge.Issuer() || claims["repository_id"] != "1" || claims["aud"] != "nx-cache" {
		t.Errorf("claims = %v", claims)
	}
	if parsed.Header["kid"] != forge.KeyID() {
		t.Errorf("kid header = %v, want %q", parsed.Header["kid"], forge.KeyID())
	}
}

func TestForgesHaveDistinctKeys(t *testing.T) {
	a, err := Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	b, err := Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })

	if a.KeyID() == b.KeyID() {
		t.Error("two forges must not share a signing key")
	}
}

func TestMintFollowsFastForward(t *testing.T) {
	forge, err := Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = forge.Close() })

	before := forge.Now()
	forge.FastForward(time.Hour)

	raw, err := forge.MintGitHub(GitHubToken{Audience: "a", RepoID: "1", Repo: "o/r", Ref: "refs/heads/main", EventName: "push"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _, err := jwt.NewParser().ParseUnverified(raw, jwt.MapClaims{})
	if err != nil {
		t.Fatal(err)
	}
	iat, err := parsed.Claims.GetIssuedAt()
	if err != nil {
		t.Fatal(err)
	}
	if iat.Time.Before(before.Add(59 * time.Minute)) {
		t.Errorf("iat = %v, want the fast-forwarded time", iat.Time)
	}
}

func TestDefaultSignerMatchesListen(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	forge, err := Listen(ln)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = forge.Close() })

	signer, err := DefaultSigner(forge.Issuer())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := signer.MintGitHub(GitHubToken{Audience: "a", RepoID: "1", Repo: "o/r", Ref: "refs/heads/main", EventName: "push"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := forge.Keypair.VerifyJWT(raw, forge.Now); err != nil {
		t.Errorf("a token from the default signer is rejected by Listen's key: %v", err)
	}
}

func getJSON(t *testing.T, url string, out any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}

func mintedClaims(t *testing.T, forge *Forge, raw string) jwt.MapClaims {
	t.Helper()
	parsed, err := forge.Keypair.VerifyJWT(raw, forge.Now)
	if err != nil {
		t.Fatalf("token does not verify: %v", err)
	}
	return parsed.Claims.(jwt.MapClaims)
}

func TestMintShapes(t *testing.T) {
	forge, err := Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = forge.Close() })

	t.Run("github", func(t *testing.T) {
		raw, err := forge.MintGitHub(GitHubToken{
			Audience: "nx-cache", RepoID: "123", Repo: "org/app", Ref: "refs/pull/7/merge",
			EventName: "pull_request", BaseRef: "main", Actor: "louis",
		})
		if err != nil {
			t.Fatal(err)
		}

		c := mintedClaims(t, forge, raw)
		want := map[string]any{
			"iss": forge.Issuer(), "aud": "nx-cache", "repository_id": "123", "repository": "org/app",
			"ref": "refs/pull/7/merge", "ref_type": "branch", "event_name": "pull_request",
			"base_ref": "main", "actor": "louis",
		}
		for key, value := range want {
			if c[key] != value {
				t.Errorf("%s = %v, want %v", key, c[key], value)
			}
		}
	})

	t.Run("gitlab", func(t *testing.T) {
		raw, err := forge.MintGitLab(GitLabToken{
			Audience: "nx-cache", ProjectID: "20", ProjectPath: "group/app", NamespaceID: "72",
			NamespacePath: "group", Ref: "feature/x", PipelineSource: "merge_request_event",
			UserLogin: "louis", UserID: "1", Protected: true,
		})
		if err != nil {
			t.Fatal(err)
		}

		c := mintedClaims(t, forge, raw)
		want := map[string]any{
			"iss": forge.Issuer(), "aud": "nx-cache",
			"project_id": "20", "project_path": "group/app", "namespace_id": "72", "namespace_path": "group",
			"ref": "feature/x", "ref_path": "refs/heads/feature/x", "ref_type": "branch", "ref_protected": "true",
			"pipeline_source": "merge_request_event", "user_login": "louis",
			"sub": "project_path:group/app:ref_type:branch:ref:feature/x",
		}
		for key, value := range want {
			if c[key] != value {
				t.Errorf("%s = %v, want %v", key, c[key], value)
			}
		}
		// GitLab has no merge request number or target branch claim.
		for _, key := range []string{"base_ref", "head_ref", "merge_request_iid"} {
			if _, ok := c[key]; ok {
				t.Errorf("unexpected claim %s", key)
			}
		}
	})

	t.Run("gitlab tag", func(t *testing.T) {
		raw, err := forge.MintGitLab(GitLabToken{Audience: "a", ProjectID: "1", ProjectPath: "g/p", Ref: "v1.0.0", RefType: "tag"})
		if err != nil {
			t.Fatal(err)
		}

		if c := mintedClaims(t, forge, raw); c["ref_path"] != "refs/tags/v1.0.0" || c["ref_type"] != "tag" {
			t.Errorf("claims = %v", c)
		}
	})

	t.Run("forgejo", func(t *testing.T) {
		raw, err := forge.MintForgejo(ForgejoToken{
			Audience: "nx-cache", RepoID: 456, Repo: "org/app", Owner: "org", OwnerID: 12,
			Ref: "refs/heads/main", EventName: "push", Actor: "louis", ActorID: 3, Protected: true,
		})
		if err != nil {
			t.Fatal(err)
		}

		c := mintedClaims(t, forge, raw)
		want := map[string]any{
			"iss": forge.Issuer(), "aud": "nx-cache", "repository": "org/app", "repository_owner": "org",
			"ref": "refs/heads/main", "ref_type": "branch", "ref_protected": true, "event_name": "push",
			"actor": "louis", "sub": "repo:org/app:ref:refs/heads/main",
			// JSON numbers decode as float64.
			"repository_id": float64(456), "repository_owner_id": float64(12), "actor_id": float64(3),
		}
		for key, value := range want {
			if c[key] != value {
				t.Errorf("%s = %v, want %v", key, c[key], value)
			}
		}
	})

	t.Run("forgejo pull request subject", func(t *testing.T) {
		raw, err := forge.MintForgejo(ForgejoToken{
			Audience: "a", RepoID: 1, Repo: "org/app", Ref: "refs/pull/7/head", EventName: "pull_request", BaseRef: "main",
		})
		if err != nil {
			t.Fatal(err)
		}

		c := mintedClaims(t, forge, raw)
		if c["sub"] != "repo:org/app:pull_request" || c["base_ref"] != "main" {
			t.Errorf("claims = %v", c)
		}
	})
}

func TestMintTimingDefaults(t *testing.T) {
	forge, err := Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = forge.Close() })

	raw, err := forge.MintGitLab(GitLabToken{Audience: "a", ProjectID: "1", ProjectPath: "g/p", Ref: "main", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}

	c := mintedClaims(t, forge, raw)
	iat, exp := c["iat"].(float64), c["exp"].(float64)
	if exp-iat != 60 || c["nbf"] != c["iat"] {
		t.Errorf("iat = %v, nbf = %v, exp = %v", c["iat"], c["nbf"], c["exp"])
	}
}
