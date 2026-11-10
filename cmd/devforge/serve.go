package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/pflag"

	"nx-caching-server/internal/devforge"
)

const defaultAddr = "127.0.0.1:9100"

func serve(args []string, stderr io.Writer) error {
	flags := pflag.NewFlagSet("serve", pflag.ContinueOnError)
	flags.SetOutput(stderr)
	addr := flags.String("addr", defaultAddr, "address to listen on")
	audience := flags.String("audience", defaultAudience, "audience shown in the config snippet")
	if err := flags.Parse(args); err != nil {
		return err
	}

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	forge, err := devforge.Listen(listener)
	if err != nil {
		return err
	}

	fmt.Fprintf(stderr, "fake forge listening on %s\n\n", forge.Issuer())
	fmt.Fprintf(stderr, "auth config snippet (run the server with AUTH_ALLOW_INSECURE_ISSUER=true):\n\n"+
		"forges:\n"+
		"  - name: dev\n"+
		"    type: github   # or gitlab, forgejo\n"+
		"    issuer: %s\n"+
		"    audience: %s\n"+
		"    repos:\n"+
		"      - id: \"%s\"\n"+
		"        name: %s\n"+
		"        default_branch: %s\n\n",
		forge.Issuer(), *audience, defaultRepoID, defaultRepoPath, defaultBranch)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	return forge.Close()
}
