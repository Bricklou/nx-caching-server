// Loads the authentication settings from the YAML file named by AUTH_CONFIG.
// Returns no config when AUTH_CONFIG is unset, meaning authentication is off.
package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	authConfigKey          = "AUTH_CONFIG"
	allowInsecureIssuerKey = "AUTH_ALLOW_INSECURE_ISSUER"
)

var (
	errEmptyAuthConfig   = errors.New("file is empty")
	errMultipleDocuments = errors.New("file must contain a single YAML document")
	errNoForges          = errors.New("at least one forge is required")
)

// FileError holds every problem found while loading a config file, so callers
// can report them one by one.
type FileError struct {
	Path     string
	Problems []error
}

func (e *FileError) Error() string {
	messages := make([]string, len(e.Problems))
	for i, problem := range e.Problems {
		messages[i] = problem.Error()
	}
	return fmt.Sprintf("auth config %s: %s", e.Path, strings.Join(messages, "; "))
}

func (e *FileError) Unwrap() []error { return e.Problems }

// flatten returns the leaf errors of a tree built with errors.Join.
func flatten(err error) []error {
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return []error{err}
	}

	var leaves []error
	for _, inner := range joined.Unwrap() {
		leaves = append(leaves, flatten(inner)...)
	}
	return leaves
}

// ValidateOptions relaxes validation rules for development.
type ValidateOptions struct {
	// AllowInsecureIssuer accepts http issuer and JWKS URLs.
	AllowInsecureIssuer bool
}

// AuthConfig lists the git forges whose OIDC tokens are accepted.
type AuthConfig struct {
	Forges []Forge `yaml:"forges"`
}

// LogValue summarizes the config so logs never contain its full content.
func (c *AuthConfig) LogValue() slog.Value {
	if c == nil {
		return slog.StringValue("disabled")
	}

	names := make([]string, len(c.Forges))
	for i, forge := range c.Forges {
		names[i] = forge.Name
	}
	return slog.GroupValue(slog.Any("forges", names))
}

// loadAuthConfig reads and decodes the file at AUTH_CONFIG, or returns nil
// when the variable is unset.
func loadAuthConfig() (*AuthConfig, error) {
	configPath := os.Getenv(authConfigKey)
	if configPath == "" {
		return nil, nil
	}

	opts, err := loadValidateOptions()
	if err != nil {
		return nil, err
	}

	file, err := os.Open(configPath)
	if err != nil {
		// The path is already carried by FileError.
		if pathErr, ok := errors.AsType[*fs.PathError](err); ok {
			err = pathErr.Err
		}
		return nil, &FileError{Path: configPath, Problems: []error{err}}
	}
	defer file.Close()

	slog.Debug("loading auth config", slog.String("path", configPath))

	config, err := decodeAuthConfig(file, opts)
	if err != nil {
		return nil, &FileError{Path: configPath, Problems: flatten(err)}
	}
	if opts.AllowInsecureIssuer {
		warnInsecure(config)
	}
	return config, nil
}

// loadValidateOptions parses AUTH_ALLOW_INSECURE_ISSUER. An invalid value is an
// error so a typo cannot silently turn the strict rule on or off.
func loadValidateOptions() (ValidateOptions, error) {
	raw := os.Getenv(allowInsecureIssuerKey)
	if raw == "" {
		return ValidateOptions{}, nil
	}

	allow, err := strconv.ParseBool(raw)
	if err != nil {
		return ValidateOptions{}, fmt.Errorf("%s: %q is not a boolean", allowInsecureIssuerKey, raw)
	}
	return ValidateOptions{AllowInsecureIssuer: allow}, nil
}

// warnInsecure logs the forges that actually use http, so a flag left on in
// production stands out.
func warnInsecure(config *AuthConfig) {
	var insecure []string
	for _, forge := range config.Forges {
		if strings.HasPrefix(forge.Issuer, "http://") || strings.HasPrefix(forge.JWKSURL, "http://") {
			insecure = append(insecure, forge.Name)
		}
	}
	slog.Warn(allowInsecureIssuerKey+" is set: http issuers are accepted, for development only",
		slog.Any("insecure_forges", insecure))
}

// ParseAuthConfig decodes one YAML document, rejecting unknown keys and an
// empty document, without validating the content. decodeAuthConfig adds validation.
func ParseAuthConfig(r io.Reader) (*AuthConfig, error) {
	decoder := yaml.NewDecoder(r)
	decoder.KnownFields(true)

	var config AuthConfig
	if err := decoder.Decode(&config); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errEmptyAuthConfig
		}
		return nil, err
	}

	var extra yaml.Node
	switch err := decoder.Decode(&extra); {
	case err == nil:
		return nil, errMultipleDocuments
	case !errors.Is(err, io.EOF):
		return nil, err
	}

	return &config, nil
}

// decodeAuthConfig parses the document, then validates it.
func decodeAuthConfig(r io.Reader, opts ValidateOptions) (*AuthConfig, error) {
	config, err := ParseAuthConfig(r)
	if err != nil {
		return nil, err
	}
	if err := config.Validate(opts); err != nil {
		return nil, err
	}
	return config, nil
}

// Validate reports every problem found in the config at once, so one startup
// attempt is enough to fix a file.
func (c *AuthConfig) Validate(opts ValidateOptions) error {
	if len(c.Forges) == 0 {
		return errNoForges
	}

	var errs []error
	names := make(map[string]bool, len(c.Forges))
	issuers := make(map[string]string, len(c.Forges))
	for i, forge := range c.Forges {
		for _, err := range forge.validate(opts) {
			errs = append(errs, fmt.Errorf("forges[%d] (%q): %w", i, forge.Name, err))
		}
		if forge.Name != "" && names[forge.Name] {
			errs = append(errs, fmt.Errorf("forges[%d]: duplicate name %q", i, forge.Name))
		}
		names[forge.Name] = true

		// The token issuer selects the forge, so it must identify exactly one.
		if other, ok := issuers[forge.Issuer]; ok && forge.Issuer != "" {
			errs = append(errs, fmt.Errorf("forges[%d] (%q): issuer already used by %q", i, forge.Name, other))
		}
		issuers[forge.Issuer] = forge.Name
	}

	return errors.Join(errs...)
}
