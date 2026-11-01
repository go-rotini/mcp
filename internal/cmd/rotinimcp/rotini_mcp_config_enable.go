package rotinimcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-rotini/rotini"

	"github.com/go-rotini/mcp"
	"github.com/go-rotini/mcp/clients"
)

var _ rotini.HandlerE = (*rotiniMcpConfigEnableHandler)(nil)

type rotiniMcpConfigEnableHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*rotiniMcpConfigEnableHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[RotiniMcpConfigEnableInputs]()
	if err != nil {
		return err
	}
	f, a := inputs.RotiniMcpConfigEnable.Flags, inputs.RotiniMcpConfigEnable.Arguments

	server, err := serverEntry(rtx.Dir(), f, a)
	if err != nil {
		return err
	}
	path, err := clients.Enable(clients.Client(a.Client), clients.Scope(f.Scope), clients.Dirs{Project: rtx.Dir()}, a.Name, server)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(rtx.Stdout, "added %s to %s\n", a.Name, path); err != nil {
		return err
	}
	scope := clients.Scope(f.Scope)
	if scope == "" {
		scope = clients.Client(a.Client).Scopes()[0]
	}
	if scope == clients.Project && machinePaths(server) {
		_, _ = fmt.Fprintf(rtx.Stderr, "note: %s names files by paths on this machine; anyone who shares the file needs an entry of their own\n", path)
	}
	if !f.Permissions {
		return nil
	}
	perms, err := toolPermissions(underDir(rtx.Dir(), f.Tools))
	if err != nil {
		return err
	}
	path, err = clients.SetPermissions(clients.Client(a.Client), clients.Dirs{Project: rtx.Dir()}, a.Name, perms)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(rtx.Stdout, "set the tool rules for %s in %s: %d allowed, %d asked\n", a.Name, path, len(perms.Allow), len(perms.Ask))
	return err
}

// toolPermissions sorts a tools file's tools by their annotations: read-only ones are allowed,
// destructive ones asked; the rest get no rule.
func toolPermissions(file string) (clients.Permissions, error) {
	doc, err := os.ReadFile(file)
	if err != nil {
		return clients.Permissions{}, err
	}
	tools, err := mcp.ParseTools(doc)
	if err != nil {
		return clients.Permissions{}, err
	}
	var p clients.Permissions
	for _, t := range tools {
		switch a := t.Annotations; {
		case a == nil:
		case a.ReadOnlyHint:
			p.Allow = append(p.Allow, t.Name)
		case a.DestructiveHint != nil && *a.DestructiveHint:
			p.Ask = append(p.Ask, t.Name)
		}
	}
	return p, nil
}

// serverEntry is how the client starts the server: rotini-mcp serve for --tools and --bin,
// or --command with its --arg words (and --tools only for --permissions).
func serverEntry(dir string, f RotiniMcpConfigEnableFlags, a RotiniMcpConfigEnableArguments) (clients.Server, error) {
	if err := checkEnv(f.Env); err != nil {
		return clients.Server{}, err
	}
	env := map[string]string{}
	for _, kv := range f.Env {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	switch {
	case f.Permissions && clients.Client(a.Client) != clients.ClaudeCode:
		return clients.Server{}, fmt.Errorf("--permissions writes Claude Code's rules; it doesn't go with %s", a.Client)
	case f.Permissions && f.Tools == "":
		return clients.Server{}, errors.New("--permissions reads the tools' annotations from --tools; give it")
	case f.Command != "" && (f.Bin != "" || (f.Tools != "" && !f.Permissions)):
		return clients.Server{}, errors.New("give either --command or --tools and --bin, not both")
	case f.Command != "":
		command, err := commandPath(dir, f.Command)
		if err != nil {
			return clients.Server{}, err
		}
		return clients.Server{Command: command, Args: f.Arg, Env: env}, nil
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

// commandPath is the --command a client runs: a path (anything with a separator) made absolute,
// since a client starts servers from a directory of its own, and checked; a bare name as given,
// for the client to find on its PATH.
func commandPath(dir, command string) (string, error) {
	if !strings.ContainsRune(command, '/') && !strings.ContainsRune(command, filepath.Separator) {
		return command, nil
	}
	abs, err := filepath.Abs(underDir(dir, command))
	if err != nil {
		return "", fmt.Errorf("resolve --command: %w", err)
	}
	if err := isFile(abs); err != nil {
		return "", fmt.Errorf("--command %s: %w", command, err)
	}
	return abs, nil
}

// machinePaths reports whether a server entry names files by absolute paths, which hold only on
// this machine.
func machinePaths(s clients.Server) bool {
	if filepath.IsAbs(s.Command) {
		return true
	}
	return slices.ContainsFunc(s.Args, filepath.IsAbs)
}
