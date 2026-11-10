// Tests the api_url setting of a forge: decoding and validation.
// It only applies to GitLab, which the server calls to find the merge request of a pipeline.
package config

import (
	"strings"
	"testing"
)

func gitlabForge(apiURL string) Forge {
	f := validForge()
	f.Name, f.Type, f.Issuer, f.APIURL = "gitlab", ForgeTypeGitlab, "https://gitlab.example.com", apiURL
	return f
}

func TestAPIURLDecodes(t *testing.T) {
	raw := `
forges:
  - name: gitlab
    type: gitlab
    issuer: https://gitlab.example.com
    audience: nx-cache
    api_url: https://gitlab.internal.example.com
    repos:
      - id: "1"
        default_branch: main
`
	cfg, err := decodeAuthConfig(strings.NewReader(raw), ValidateOptions{})

	if err != nil || cfg.Forges[0].APIURL != "https://gitlab.internal.example.com" {
		t.Errorf("got (%+v, %v)", cfg, err)
	}
}

func TestAPIURLValidation(t *testing.T) {
	github := validForge()
	github.APIURL = "https://gl.example.com"

	tests := []struct {
		name  string
		forge Forge
		want  string // empty means valid
	}{
		{"unset", gitlabForge(""), ""},
		{"https url", gitlabForge("https://gl.example.com"), ""},
		{"http url", gitlabForge("http://gl.example.com"), "api_url"},
		{"not a url", gitlabForge("gl"), "api_url"},
		{"github forge", github, "only used by gitlab"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := (&AuthConfig{Forges: []Forge{tt.forge}}).Validate(ValidateOptions{})

			switch {
			case tt.want == "" && err != nil:
				t.Errorf("valid forge rejected: %v", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Errorf("error = %v, want one mentioning %q", err, tt.want)
			}
		})
	}
}

func TestAPIURLAcceptsHTTPWhenInsecureIssuersAreAllowed(t *testing.T) {
	f := gitlabForge("http://127.0.0.1:8080")
	f.Issuer = "http://127.0.0.1:8080"

	if err := (&AuthConfig{Forges: []Forge{f}}).Validate(ValidateOptions{AllowInsecureIssuer: true}); err != nil {
		t.Errorf("rejected: %v", err)
	}
}
