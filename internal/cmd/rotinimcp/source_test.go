package rotinimcp

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// sourceModule writes a module whose program, ./cmd/hello, prints its arguments, and a tools
// file with one tool, hello_greet, that names it.
func sourceModule(t *testing.T, program string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/hello\n\ngo 1.27\n",
		"cmd/hello/main.go": `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() { fmt.Println("hello from source:", strings.Join(os.Args[1:], " ")) }
`,
		"tools/mcp.json": `{"_meta": {"dev.rotini/program": ` + program + `}, "tools": [` +
			`{"name": "hello_greet", "inputSchema": {"type": "object"}, "_meta": {"dev.rotini/invoke": {"path": ["greet"]}}}]}`,
	}
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Without a program, serve builds the one the tools file names, and runs that build for each
// call, in the -C directory.
func TestServe_fromSource(t *testing.T) {
	dir := sourceModule(t, `{"name": "hello", "main": "./cmd/hello"}`)
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan int, 1)
	go func() {
		code, _ := NewProgram().
			WithDir(t.TempDir()).
			WithStdin(sr).
			WithStdout(sw).
			WithStderr(io.Discard).
			WithContext(ctx).
			WithExit(func(int) {}).
			Run([]string{"serve", "-C", dir})
		_ = sw.Close()
		done <- code
	}()
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil).
		Connect(ctx, &sdk.IOTransport{Reader: cr, Writer: cw}, nil)
	if err != nil {
		t.Fatalf("connect: %v (serve exited %v)", err, <-done)
	}
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "hello_greet"})
	if err != nil {
		t.Fatal(err)
	}
	if tc, ok := res.Content[0].(*sdk.TextContent); !ok || tc.Text != "hello from source: greet\n" {
		t.Fatalf("result = %+v", res.Content)
	}
	if info, _ := res.Meta["io.modelcontextprotocol/serverInfo"].(map[string]any); info["name"] != "hello" {
		t.Errorf("server info = %v", info)
	}
	_ = cs.Close()
	cancel()
	if code := <-done; code != 0 {
		t.Errorf("serve exited %d", code)
	}
}

func TestServe_fromSourceErrors(t *testing.T) {
	for _, tc := range []struct {
		program, want string
	}{
		{`{}`, "doesn't name its program"},
		{`{"name": "hello"}`, "doesn't name hello's main package"},
		{`{"name": "hello", "main": "./cmd/nope"}`, "build hello from ./cmd/nope"},
	} {
		code, _, errOut := runCLI(t, sourceModule(t, tc.program), "serve")
		if code == 0 || !strings.Contains(errOut, tc.want) {
			t.Errorf("%s: %d %q", tc.program, code, errOut)
		}
	}
	dir := sourceModule(t, `{"name": "hello", "main": "./cmd/hello"}`)
	if err := os.Remove(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runCLI(t, dir, "serve")
	if code == 0 || !strings.Contains(errOut, "isn't in a Go module") {
		t.Errorf("no module: %d %q", code, errOut)
	}
}

// Without a program, add writes rotini-mcp serve -C for this directory, named after the program
// the tools file names.
func TestAdd_fromSource(t *testing.T) {
	isolate(t)
	dir := sourceModule(t, `{"name": "hello", "main": "./cmd/hello"}`)
	code, out, errOut := runCLI(t, t.TempDir(), "-C", dir, "add", "claude-code")
	if code != 0 || !strings.Contains(out, "added hello to") {
		t.Fatalf("add: %d %q %q", code, out, errOut)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		MCPServers map[string]struct {
			Args []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	want := "serve -C " + dir + " --tools " + filepath.Join(dir, "tools", "mcp.json")
	if got := strings.Join(doc.MCPServers["hello"].Args, " "); got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}
