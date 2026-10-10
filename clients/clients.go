// Package clients adds, removes and lists MCP servers in the configuration files of MCP
// clients: Claude Desktop, Claude Code, Cursor and VS Code.
//
// A file is changed only when asked. Before an existing file is rewritten it is copied to
// <file>.bak; the rewrite keeps every other key but not comments.
package clients

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/go-rotini/jsonc"
)

// Client is an MCP client whose configuration this package edits.
type Client string

// The clients.
const (
	ClaudeDesktop Client = "claude-desktop"
	ClaudeCode    Client = "claude-code"
	Cursor        Client = "cursor"
	VSCode        Client = "vscode"
)

// Clients lists every client.
func Clients() []Client { return []Client{ClaudeDesktop, ClaudeCode, Cursor, VSCode} }

// Scope is which of a client's files: the user's own, or the project's (in the working
// directory).
type Scope string

// The scopes.
const (
	User    Scope = "user"
	Project Scope = "project"
)

// Server is how a client starts an MCP server over stdio.
type Server struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// Dirs are the directories files are found under. A zero field means the default: the user's
// home directory, the user's configuration directory (os.UserConfigDir) and the working
// directory.
type Dirs struct {
	Home, Config, Project string
}

// ErrScope is a scope a client has no file for.
var ErrScope = errors.New("clients: the client has no file for this scope")

// Scopes lists the scopes a client has a file for; the first is the default.
func (c Client) Scopes() []Scope {
	switch c {
	case ClaudeDesktop:
		return []Scope{User}
	case ClaudeCode:
		return []Scope{Project}
	case Cursor, VSCode:
		return []Scope{User, Project}
	}
	return nil
}

// Path is the client's configuration file for a scope ("" for its default).
func (c Client) Path(scope Scope, dirs Dirs) (string, error) {
	scopes := c.Scopes()
	if scopes == nil {
		return "", fmt.Errorf("clients: unknown client %q", c)
	}
	if scope == "" {
		scope = scopes[0]
	}
	if !slices.Contains(scopes, scope) {
		return "", fmt.Errorf("%w: %s has no %s file", ErrScope, c, scope)
	}
	dirs, err := dirs.resolve()
	if err != nil {
		return "", err
	}
	switch {
	case c == ClaudeDesktop:
		return filepath.Join(dirs.Config, "Claude", "claude_desktop_config.json"), nil
	case c == ClaudeCode:
		return filepath.Join(dirs.Project, ".mcp.json"), nil
	case c == Cursor && scope == User:
		return filepath.Join(dirs.Home, ".cursor", "mcp.json"), nil
	case c == Cursor:
		return filepath.Join(dirs.Project, ".cursor", "mcp.json"), nil
	case c == VSCode && scope == User:
		return filepath.Join(dirs.Config, "Code", "User", "mcp.json"), nil
	default:
		return filepath.Join(dirs.Project, ".vscode", "mcp.json"), nil
	}
}

// serversKey is the key a client's file lists its servers under.
func (c Client) serversKey() string {
	if c == VSCode {
		return "servers"
	}
	return "mcpServers"
}

// Enable adds (or replaces) the server called name in the client's file, creating the file if
// it doesn't exist.
func Enable(c Client, scope Scope, dirs Dirs, name string, s Server) (path string, err error) {
	if name == "" || s.Command == "" {
		return "", errors.New("clients: a server needs a name and a command")
	}
	path, err = c.Path(scope, dirs)
	if err != nil {
		return "", err
	}
	doc, exists, err := read(path)
	if err != nil {
		return "", err
	}
	servers, err := serversOf(doc, c.serversKey(), path)
	if err != nil {
		return "", err
	}
	entry := map[string]any{"command": s.Command}
	if c == VSCode {
		entry["type"] = "stdio"
	}
	if len(s.Args) > 0 {
		entry["args"] = s.Args
	}
	if len(s.Env) > 0 {
		entry["env"] = s.Env
	}
	servers[name] = entry
	doc[c.serversKey()] = servers
	return path, write(path, doc, exists)
}

// Disable removes the server called name from the client's file. It reports whether the server
// was there; a missing file has no servers.
func Disable(c Client, scope Scope, dirs Dirs, name string) (path string, removed bool, err error) {
	path, err = c.Path(scope, dirs)
	if err != nil {
		return "", false, err
	}
	doc, exists, err := read(path)
	if err != nil || !exists {
		return path, false, err
	}
	servers, err := serversOf(doc, c.serversKey(), path)
	if err != nil {
		return "", false, err
	}
	if _, ok := servers[name]; !ok {
		return path, false, nil
	}
	delete(servers, name)
	doc[c.serversKey()] = servers
	return path, true, write(path, doc, exists)
}

// List reads the servers in the client's file, by name. A missing file has none.
func List(c Client, scope Scope, dirs Dirs) (path string, servers map[string]Server, err error) {
	path, err = c.Path(scope, dirs)
	if err != nil {
		return "", nil, err
	}
	doc, _, err := read(path)
	if err != nil {
		return "", nil, err
	}
	raw, err := serversOf(doc, c.serversKey(), path)
	if err != nil {
		return "", nil, err
	}
	servers = map[string]Server{}
	for _, name := range slices.Sorted(maps.Keys(raw)) {
		b, err := json.Marshal(raw[name])
		if err != nil {
			return "", nil, fmt.Errorf("clients: %s: server %q: %w", path, name, err)
		}
		var s Server
		if json.Unmarshal(b, &s) == nil {
			servers[name] = s
		}
	}
	return path, servers, nil
}

func (d Dirs) resolve() (Dirs, error) {
	var err error
	if d.Home == "" {
		if d.Home, err = os.UserHomeDir(); err != nil {
			return d, fmt.Errorf("clients: %w", err)
		}
	}
	if d.Config == "" {
		if d.Config, err = os.UserConfigDir(); err != nil {
			return d, fmt.Errorf("clients: %w", err)
		}
	}
	if d.Project == "" {
		if d.Project, err = os.Getwd(); err != nil {
			return d, fmt.Errorf("clients: %w", err)
		}
	}
	return d, nil
}

// read loads a configuration file (JSON with comments allowed); a missing file is empty.
func read(path string) (doc map[string]any, exists bool, err error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("clients: %w", err)
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return map[string]any{}, true, nil
	}
	plain, err := jsonc.ToJSON(b)
	if err != nil {
		return nil, true, fmt.Errorf("clients: %s isn't JSON: %w", path, err)
	}
	if err := json.Unmarshal(plain, &doc); err != nil || doc == nil {
		return nil, true, fmt.Errorf("clients: %s isn't a JSON object", path)
	}
	return doc, true, nil
}

func serversOf(doc map[string]any, key, path string) (map[string]any, error) {
	v, ok := doc[key]
	if !ok || v == nil {
		return map[string]any{}, nil
	}
	servers, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("clients: %s: %q isn't an object", path, key)
	}
	return servers, nil
}

// write saves a configuration file, first copying an existing one to <file>.bak.
func write(path string, doc map[string]any, exists bool) error {
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("clients: %w", err)
	}
	if exists {
		old, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("clients: %w", err)
		}
		if err := os.WriteFile(path+".bak", old, 0o600); err != nil {
			return fmt.Errorf("clients: back up %s: %w", path, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("clients: %w", err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("clients: %w", err)
	}
	return nil
}
