// Package clients adds, removes and lists MCP servers in the configuration files of MCP
// clients: Claude Desktop, Claude Code, Cursor and VS Code.
//
// A file is changed only when asked. Before an existing file is rewritten it is copied to
// <file>.bak, replacing an earlier backup, so the backup is the file before the last change.
// The rewrite keeps every other key, in its order, and numbers as written, but not comments. It
// replaces the file in one step, keeping its mode; when the file is a symbolic link, the file it
// links to is replaced and the link kept.
package clients

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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
// home directory, the user's configuration directory ([os.UserConfigDir]) and the working
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

// Enable adds the server called name to the client's file, creating the file if it doesn't
// exist. When the file already has a server of that name, its command, args and type are set
// and its env replaced when s has one; its other settings (such as disabled, autoApprove, cwd
// or timeout) are kept. It returns the file's path.
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
	entry, _ := servers.get(name).(*object)
	if entry == nil {
		entry = newObject()
	}
	if c == VSCode {
		entry.set("type", "stdio")
	}
	entry.set("command", s.Command)
	if len(s.Args) > 0 {
		entry.set("args", s.Args)
	} else {
		entry.delete("args")
	}
	if len(s.Env) > 0 {
		entry.set("env", s.Env)
	}
	servers.set(name, entry)
	doc.set(c.serversKey(), servers)
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
	if _, ok := servers.vals[name]; !ok {
		return path, false, nil
	}
	servers.delete(name)
	doc.set(c.serversKey(), servers)
	return path, true, write(path, doc, exists)
}

// List reads the servers in the client's file, by name. A missing file has none, and an entry
// that doesn't read as a [Server] is left out.
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
	for _, name := range raw.keys {
		b, err := json.Marshal(raw.vals[name])
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
// Numbers are kept as written, and members in the order they are written.
func read(path string) (doc *object, exists bool, err error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return newObject(), false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("clients: %w", err)
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return newObject(), true, nil
	}
	plain, err := jsonc.ToJSON(b)
	if err != nil {
		return nil, true, fmt.Errorf("clients: %s isn't JSON: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(plain))
	dec.UseNumber()
	v, err := decodeValue(dec)
	doc, ok := v.(*object)
	if err != nil || !ok {
		return nil, true, fmt.Errorf("clients: %s isn't a JSON object", path)
	}
	return doc, true, nil
}

func serversOf(doc *object, key, path string) (*object, error) {
	v := doc.get(key)
	if v == nil {
		return newObject(), nil
	}
	servers, ok := v.(*object)
	if !ok {
		return nil, fmt.Errorf("clients: %s: %q isn't an object", path, key)
	}
	return servers, nil
}

// write saves a configuration file, first copying an existing one to <file>.bak (replacing an
// earlier backup). The new file is written beside it and renamed over it, keeping its mode, so
// a failed write leaves the file as it was. When path is a symbolic link, the file it links to
// is the one replaced, and the link is kept.
func write(path string, doc *object, exists bool) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("clients: %w", err)
	}
	target, err := linkTarget(path)
	if err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if exists {
		info, err := os.Stat(target)
		if err != nil {
			return fmt.Errorf("clients: %w", err)
		}
		mode = info.Mode().Perm()
		old, err := os.ReadFile(target)
		if err != nil {
			return fmt.Errorf("clients: %w", err)
		}
		if err := os.WriteFile(path+".bak", old, 0o600); err != nil {
			return fmt.Errorf("clients: back up %s: %w", path, err)
		}
	}
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("clients: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(target)+".*.tmp")
	if err != nil {
		return fmt.Errorf("clients: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // gone after the rename; cleans up after a failure
	_, err = tmp.Write(buf.Bytes())
	err = errors.Join(err, tmp.Chmod(mode), tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), target)
	}
	if err != nil {
		return fmt.Errorf("clients: write %s: %w", path, err)
	}
	return nil
}

// linkTarget is the file path names: path itself, or the file a symbolic link at path leads
// to, which need not exist yet.
func linkTarget(path string) (string, error) {
	target := path
	for range 40 {
		info, err := os.Lstat(target)
		if errors.Is(err, os.ErrNotExist) || (err == nil && info.Mode()&os.ModeSymlink == 0) {
			return target, nil
		}
		if err != nil {
			return "", fmt.Errorf("clients: %w", err)
		}
		dest, err := os.Readlink(target)
		if err != nil {
			return "", fmt.Errorf("clients: %w", err)
		}
		if !filepath.IsAbs(dest) {
			dest = filepath.Join(filepath.Dir(target), dest)
		}
		target = dest
	}
	return "", fmt.Errorf("clients: %s: too many symbolic links", path)
}

// object is a JSON object that keeps its members in the order they were read or added.
type object struct {
	keys []string
	vals map[string]any
}

func newObject() *object { return &object{vals: map[string]any{}} }

// get returns the member named k, or nil.
func (o *object) get(k string) any { return o.vals[k] }

// set sets the member named k, in its place when it exists, else last.
func (o *object) set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *object) delete(k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	o.keys = slices.DeleteFunc(o.keys, func(s string) bool { return s == k })
}

// MarshalJSON writes the members in order.
func (o *object) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := enc.Encode(k); err != nil {
			return nil, err
		}
		buf.WriteByte(':')
		if err := enc.Encode(o.vals[k]); err != nil {
			return nil, err
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// decodeValue reads one JSON value, objects as *object and numbers as json.Number. A member
// written twice keeps its first place and its last value.
func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch tok {
	case json.Delim('{'):
		o := newObject()
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			k, _ := kt.(string)
			v, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			o.set(k, v)
		}
		_, err := dec.Token()
		return o, err
	case json.Delim('['):
		arr := []any{}
		for dec.More() {
			v, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		_, err := dec.Token()
		return arr, err
	}
	return tok, nil
}
