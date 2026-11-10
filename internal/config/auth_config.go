// ABOUTME: Loads the authentication settings from the YAML file named by AUTH_CONFIG.
// ABOUTME: Returns no config when AUTH_CONFIG is unset, meaning authentication is off.
package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const authConfigKey = "AUTH_CONFIG"

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

	config, err := decodeAuthConfig(file)
	if err != nil {
		return nil, &FileError{Path: configPath, Problems: flatten(err)}
	}
	return config, nil
}

// decodeAuthConfig decodes exactly one YAML document and rejects unknown keys.
// An empty document is an error so a blank file can never disable auth.
func decodeAuthConfig(r io.Reader) (*AuthConfig, error) {
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

	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &config, nil
}

// Validate reports every problem found in the config at once, so one startup
// attempt is enough to fix a file.
func (c *AuthConfig) Validate() error {
	if len(c.Forges) == 0 {
		return errNoForges
	}

	var errs []error
	names := make(map[string]bool, len(c.Forges))
	issuers := make(map[string]string, len(c.Forges))
	for i, forge := range c.Forges {
		for _, err := range forge.validate() {
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
