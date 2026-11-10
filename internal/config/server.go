// Settings of the HTTP listener.
package config

import (
	"fmt"
	"os"
	"strconv"
)

const (
	portKey     = "PORT"
	defaultPort = 8090
)

// Server configures the HTTP listener.
type Server struct {
	Port int
}

// Addr is the listen address, on every interface.
func (s Server) Addr() string {
	return ":" + strconv.Itoa(s.Port)
}

func loadServer() (Server, error) {
	raw := os.Getenv(portKey)
	if raw == "" {
		return Server{Port: defaultPort}, nil
	}

	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return Server{}, fmt.Errorf("%s: %q must be a port number between 1 and 65535", portKey, raw)
	}
	return Server{Port: port}, nil
}
