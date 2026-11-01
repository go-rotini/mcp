package rotinimcp

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-rotini/rotini"

	"github.com/go-rotini/mcp"
	"github.com/go-rotini/mcp/clients"
)

var _ rotini.HandlerE = (*rotiniMcpAddHandler)(nil)

type rotiniMcpAddHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*rotiniMcpAddHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[RotiniMcpAddInputs]()
	if err != nil {
		return err
	}
	f, a := inputs.RotiniMcpAdd.Flags, inputs.RotiniMcpAdd.Arguments
	client := clients.Client(a.Client)
	if f.Permissions && client != clients.ClaudeCode {
		return fmt.Errorf("--permissions writes Claude Code's rules; it doesn't go with %s", a.Client)
	}

	server, err := serverEntry(ctx, rtx.Dir(), f, a)
	if err != nil {
		return err
	}
	name := cmp.Or(f.Name, serverName(a.Program))
	dirs := clients.Dirs{Project: rtx.Dir()}
	path, err := clients.Add(client, clients.Scope(f.Scope), dirs, name, server)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(rtx.Stdout, "added %s to %s\n", name, path); err != nil {
		return err
	}
	scope := clients.Scope(f.Scope)
	if scope == "" {
		scope = client.Scopes()[0]
	}
	if scope == clients.Project && machinePaths(server) {
		_, _ = fmt.Fprintf(rtx.Stderr, "note: %s names files by paths on this machine; anyone who shares the file needs an entry of their own\n", path)
	}
	if !f.Permissions {
		return nil
	}
	perms, err := toolPermissions(rtx.Dir(), f.Tools)
	if err != nil {
		return err
	}
	path, err = clients.SetPermissions(client, dirs, name, perms)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(rtx.Stdout, "set the tool rules for %s in %s: %d allowed, %d asked\n", name, path, len(perms.Allow), len(perms.Ask))
	return err
}

// serverEntry is how the client starts the server: the program with its arguments, for a program
// serving itself, or rotini-mcp serve for the program and its tools file.
func serverEntry(ctx context.Context, dir string, f RotiniMcpAddFlags, a RotiniMcpAddArguments) (clients.Server, error) {
	if err := checkEnv(f.Env); err != nil {
		return clients.Server{}, err
	}
	env := map[string]string{}
	for _, kv := range f.Env {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	if len(a.Args) > 0 {
		command, err := commandPath(dir, a.Program)
		if err != nil {
			return clients.Server{}, err
		}
		return clients.Server{Command: command, Args: a.Args, Env: env}, nil
	}
	if _, err := readTools(dir, f.Tools); err != nil {
		return clients.Server{}, err
	}
	tools, err := filepath.Abs(underDir(dir, f.Tools))
	if err != nil {
		return clients.Server{}, fmt.Errorf("resolve --tools: %w", err)
	}
	bin, err := program(dir, a.Program)
	if err != nil {
		return clients.Server{}, err
	}
	command, args, err := selfCommand(ctx, dir)
	if err != nil {
		return clients.Server{}, err
	}
	return clients.Server{Command: command, Args: append(args, "serve", bin, "--tools", tools), Env: env}, nil
}

// selfCommand is how a client runs this rotini-mcp: its executable's path, or, when that is in
// Go's build cache (go tool or go run), `go -C <module> tool rotini-mcp`, since the cache can be
// cleaned. Run from the cache outside a module that declares the tool, it is an error.
func selfCommand(ctx context.Context, dir string) (command string, args []string, err error) {
	self, err := os.Executable()
	if err != nil {
		return "", nil, fmt.Errorf("find rotini-mcp: %w", err)
	}
	gocache, gomod := goEnv(ctx, dir)
	if !under(self, gocache) {
		return self, nil, nil
	}
	if gomod == "" || gomod == os.DevNull {
		return "", nil, errors.New(notToolMessage)
	}
	module := filepath.Dir(gomod)
	if _, err := goCommand(ctx, module, "tool", "-n", "rotini-mcp"); err != nil {
		return "", nil, errors.New(notToolMessage)
	}
	return "go", []string{"-C", module, "tool", "rotini-mcp"}, nil
}

const notToolMessage = "this rotini-mcp runs from Go's build cache, which can be cleaned, so a client can't keep running it; " +
	"install it (go install github.com/go-rotini/mcp/cmd/rotini-mcp@latest) or declare it as a tool of this module (go get -tool github.com/go-rotini/mcp/cmd/rotini-mcp)"

// goEnv is the go command's GOCACHE and GOMOD in dir; both are empty without a go command, whose
// absence means the executable can't be a go tool build.
func goEnv(ctx context.Context, dir string) (gocache, gomod string) {
	out, err := goCommand(ctx, dir, "env", "GOCACHE", "GOMOD")
	if err != nil {
		return "", ""
	}
	gocache, gomod, _ = strings.Cut(strings.TrimSpace(out), "\n")
	return gocache, gomod
}

// goCommand runs the go command in dir and returns its stdout.
func goCommand(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	return out.String(), err
}

// under reports whether path is inside dir, following symbolic links in both.
func under(path, dir string) bool {
	if dir == "" {
		return false
	}
	if p, err := filepath.EvalSymlinks(path); err == nil {
		path = p
	}
	if d, err := filepath.EvalSymlinks(dir); err == nil {
		dir = d
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// serverName is the name a server gets by default: the program's base name, without .exe.
func serverName(program string) string {
	return strings.TrimSuffix(filepath.Base(program), ".exe")
}

// toolPermissions sorts a tools file's tools by their annotations: read-only ones are allowed,
// destructive ones asked; the rest get no rule.
func toolPermissions(dir, file string) (clients.Permissions, error) {
	doc, err := readTools(dir, file)
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

// commandPath is the program a client runs itself: a path (anything with a separator) made
// absolute, since a client starts servers from a directory of its own, and checked; a bare name
// as given, for the client to find on its PATH.
func commandPath(dir, command string) (string, error) {
	if !strings.ContainsRune(command, '/') && !strings.ContainsRune(command, filepath.Separator) {
		return command, nil
	}
	abs, err := filepath.Abs(underDir(dir, command))
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", command, err)
	}
	if err := isFile(abs); err != nil {
		return "", fmt.Errorf("%s: %w", command, err)
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
