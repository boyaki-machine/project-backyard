// pb-mcp-bridge is a local, stdio-only bridge for Codex and PB.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/boyaki-machine/project-backyard/server/internal/mcpbridge"
)

func main() {
	var cfg mcpbridge.Config
	flag.StringVar(&cfg.URL, "url", "", "PB MCP HTTPS URL")
	flag.StringVar(&cfg.TokenEnvName, "token-env", "", "PB bearer token environment variable name")
	flag.StringVar(&cfg.CAFile, "ca-file", "", "additional PEM CA certificate bundle")
	flag.Parse()
	if err := mcpbridge.Run(context.Background(), os.Stdin, os.Stdout, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "pb-mcp-bridge:", err)
		os.Exit(1)
	}
}
