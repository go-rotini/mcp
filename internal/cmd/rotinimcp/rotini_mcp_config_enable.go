package rotinimcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-rotini/rotini"

	"github.com/go-rotini/mcp/clients"
)

var _ rotini.Handler = (*rotiniMcpConfigEnableHandler)(nil)

type rotiniMcpConfigEnableHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*rotiniMcpConfigEnableHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[RotiniMcpConfigEnableInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	f, a := inputs.RotiniMcpConfigEnable.Flags, inputs.RotiniMcpConfigEnable.Arguments

	server, err := serverEntry(rtx.Dir(), f)
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	path, err := clients.Enable(clients.Client(a.Client), clients.Scope(f.Scope), clients.Dirs{Project: rtx.Dir()}, a.Name, server)
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	if _, err := fmt.Fprintf(rtx.Stdout, "added %s to %s\n", a.Name, path); err != nil {
		rtx.HaltWith(err)
	}
}

// serverEntry is how the client starts the server: rotini-mcp serve for --tools and --bin,
// or --command with its --arg words.
func serverEntry(dir string, f RotiniMcpConfigEnableFlags) (clients.Server, error) {
	if err := checkEnv(f.Env); err != nil {
		return clients.Server{}, err
	}
	env := map[string]string{}
	for _, kv := range f.Env {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	switch {
	case f.Command != "" && (f.Tools != "" || f.Bin != ""):
		return clients.Server{}, errors.New("give either --command or --tools and --bin, not both")
	case f.Command != "":
		return clients.Server{Command: f.Command, Args: f.Arg, Env: env}, nil
	case f.Tools == "" || f.Bin == "":
		return clients.Server{}, errors.New("give --tools and --bin (rotini-mcp serves them), or --command")
	case len(f.Arg) > 0:
		return clients.Server{}, errors.New("--arg goes with --command")
	}
	self, err := os.Executable()
	if err != nil {
		return clients.Server{}, fmt.Errorf("find rotini-mcp: %w", err)
	}
	tools, err := filepath.Abs(underDir(dir, f.Tools))
	if err != nil {
		return clients.Server{}, fmt.Errorf("resolve --tools: %w", err)
	}
	bin, err := program(dir, f.Bin)
	if err != nil {
		return clients.Server{}, err
	}
	return clients.Server{Command: self, Args: []string{"serve", "--tools", tools, "--bin", bin}, Env: env}, nil
}
