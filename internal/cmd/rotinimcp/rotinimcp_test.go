package rotinimcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func runCLI(t *testing.T, dir string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code, _ = NewProgram(Handlers()).
		WithDir(dir).
		WithStdout(&out).
		WithStderr(&errOut).
		WithExit(func(int) {}).
		Run(args)
	return code, out.String(), errOut.String()
}

func TestConfig_commandServer(t *testing.T) {
	dir := t.TempDir()
	code, out, errOut := runCLI(t, dir, "config", "enable", "claude-code", "tasks", "--command", "taskr", "--arg", "mcp", "--env", "TASKR_TOKEN=x=y")
	if code != 0 || !strings.Contains(out, "added tasks to") {
		t.Fatalf("enable: %d %q %q", code, out, errOut)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	s := doc.MCPServers["tasks"]
	if s.Command != "taskr" || strings.Join(s.Args, " ") != "mcp" || s.Env["TASKR_TOKEN"] != "x=y" {
		t.Fatalf("entry = %+v", s)
	}
	code, out, _ = runCLI(t, dir, "config", "list", "claude-code")
	if code != 0 || out != "tasks\ttaskr mcp\n" {
		t.Fatalf("list: %d %q", code, out)
	}
	code, out, _ = runCLI(t, dir, "config", "disable", "claude-code", "tasks")
	if code != 0 || !strings.Contains(out, "removed tasks") {
		t.Fatalf("disable: %d %q", code, out)
	}
	code, out, _ = runCLI(t, dir, "config", "disable", "claude-code", "tasks")
	if code != 0 || !strings.Contains(out, "no server named tasks") {
		t.Fatalf("disable again: %d %q", code, out)
	}
}

func TestConfig_serveServer(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"mcp.json", "taskr"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	code, _, errOut := runCLI(t, dir, "config", "enable", "cursor", "taskr", "--scope", "project", "--tools", "mcp.json", "--bin", "taskr")
	if code != 0 {
		t.Fatalf("enable: %d %q", code, errOut)
	}
	code, out, _ := runCLI(t, dir, "config", "list", "cursor", "--scope", "project")
	want := " serve --tools " + filepath.Join(dir, "mcp.json") + " --bin " + filepath.Join(dir, "taskr") + "\n"
	if code != 0 || !strings.HasPrefix(out, "taskr\t") || !strings.HasSuffix(out, want) {
		t.Fatalf("list: %d %q, want it to end %q", code, out, want)
	}
}

func TestConfig_errors(t *testing.T) {
	dir := t.TempDir()
	for name, args := range map[string][]string{
		"nothing":   {"config", "enable", "claude-code", "x"},
		"both":      {"config", "enable", "claude-code", "x", "--command", "c", "--tools", "go.mod", "--bin", "go.mod"},
		"bad scope": {"config", "enable", "claude-desktop", "x", "--command", "c", "--scope", "project"},
		"client":    {"config", "list", "emacs"},
		"env":       {"config", "enable", "claude-code", "x", "--command", "c", "--env", "NOEQUALS"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if code, _, _ := runCLI(t, dir, args...); code == 0 {
				t.Fatal("succeeded")
			}
		})
	}
}

func TestServe_unknownTool(t *testing.T) {
	code, _, errOut := runCLI(t, "../../..", "serve", "--tools", "testdata/fake.mcp.json", "--bin", "go", "--tool", "nope")
	if code == 0 || !strings.Contains(errOut, "no such tool") {
		t.Fatalf("%d %q", code, errOut)
	}
}

func TestServe_stdio(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses echo as the program")
	}
	echo, err := exec.LookPath("echo")
	if err != nil {
		t.Skip("no echo on PATH")
	}
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan int, 1)
	go func() {
		code, _ := NewProgram(Handlers()).
			WithDir("../../..").
			WithStdin(sr).
			WithStdout(sw).
			WithStderr(io.Discard).
			WithContext(ctx).
			WithExit(func(int) {}).
			Run([]string{"serve", "--tools", "testdata/fake.mcp.json", "--bin", echo, "--tool", "fake_plain"})
		done <- code
	}()
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil).
		Connect(ctx, &sdk.IOTransport{Reader: cr, Writer: cw}, nil)
	if err != nil {
		t.Fatal(err)
	}
	list, err := cs.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 1 {
		t.Fatalf("tools: %v %v", list, err)
	}
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "fake_plain"})
	if err != nil {
		t.Fatal(err)
	}
	if tc, ok := res.Content[0].(*sdk.TextContent); !ok || tc.Text != "plain\n" {
		t.Fatalf("result = %+v", res.Content)
	}
	_ = cs.Close()
	cancel()
	if code := <-done; code != 0 {
		t.Errorf("serve exited %d", code)
	}
}
