//go:generate go run github.com/go-rotini/rotini/cmd/rotini@v1.4.3 generate ./.rotini.spec.yaml --config ./.rotini.conf.yaml

// Command rotini-mcp serves a rotini program's tools over MCP, and adds that server to MCP
// clients' configuration.
package main

import (
	"runtime/debug"

	cmd "github.com/go-rotini/mcp/internal/cmd/rotinimcp"
)

// go build -ldflags "-X main.version=1.2.3" ./cmd/...
var version = ""

func main() {
	if version == "" {
		version = "(devel)"
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
			version = info.Main.Version
		}
	}
	cmd.NewProgram().
		WithVersion(version).
		Execute()
}
