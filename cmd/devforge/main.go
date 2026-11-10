// Local fake git forge for developing authentication without a CI job.
// `serve` runs a mock OIDC issuer, `token` prints a GitHub, GitLab or Forgejo shaped token.
package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "devforge:", err)
		os.Exit(1)
	}
}

const usageText = `usage:
  devforge serve [--addr ADDR] [--audience AUD]
  devforge token FORGE [SCENARIO] [flags]

FORGE     github | gitlab | forgejo
SCENARIO  push (default) | pr | tag | untrusted

The issuer, audience and repository come from the first forge of that type in
the auth config (--config, or $AUTH_CONFIG). Without a config, built-in
development defaults are used.

examples:
  devforge token github
  devforge token gitlab pr --branch feature/login
  devforge token forgejo pr --number 7 --base release/1.0
  devforge token github tag --tag v2.0.0`

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usageText)
		return fmt.Errorf("missing command")
	}

	switch args[0] {
	case "serve":
		return serve(args[1:], stderr)
	case "token":
		return token(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprintln(stdout, usageText)
		return nil
	}

	fmt.Fprintln(stderr, usageText)
	return fmt.Errorf("unknown command %q", args[0])
}
