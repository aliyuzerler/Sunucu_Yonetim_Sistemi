//go:build unix

package main

import (
	"fmt"
	"os"
)

// runMain is the unix CLI entry.
func runMain() int {
	if len(os.Args) < 2 {
		usage()
		return 2
	}
	if err := dispatch(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "server-agent %s: %v\n", os.Args[1], err)
		return 1
	}
	return 0
}

// defaultAgentDir is the unix agent state dir.
func defaultAgentDir() string { return "/var/lib/server-control-agent" }

// defaultBrokerEndpoint is the unix broker socket path.
func defaultBrokerEndpoint() string { return "/run/server-control/privd.sock" }
