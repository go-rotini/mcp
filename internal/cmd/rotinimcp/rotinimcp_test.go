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
	code, _, errOut := runCLI(t, dir, "config", "enable", "cursor", "taskr", "--scope", "project", "--tools", "mcp.json", "--bin", "./taskr")
	if code != 0 {
		t.Fatalf("enable: %d %q", code, errOut)
	}
	code, out, _ := runCLI(t, dir, "config", "list", "cursor", "--scope", "project")
	want := " serve --tools " + filepath.Join(dir, "mcp.json") + " --bin " + filepath.Join(dir, "taskr") + "\n"
	if code != 0 || !strings.HasPrefix(out, "taskr\t") || !strings.HasSuffix(out, want) {
		t.Fatalf("list: %d %q, want it to end %q", code, out, want)
	}
}

// A bare --bin is a name on PATH, as for serve.
func TestConfig_serveServerBinOnPath(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runCLI(t, dir, "config", "enable", "claude-code", "g", "--tools", "mcp.json", "--bin", "go")
	if code != 0 {
		t.Fatalf("enable: %d %q", code, errOut)
	}
	code, out, _ := runCLI(t, dir, "config", "list", "claude-code")
	if want := " --bin " + goBin + "\n"; code != 0 || !strings.HasSuffix(out, want) {
		t.Fatalf("list: %d %q, want it to end %q", code, out, want)
	}
	code, _, errOut = runCLI(t, dir, "config", "enable", "claude-code", "g", "--tools", "mcp.json", "--bin", "no-such-program-on-path")
	if code == 0 || !strings.Contains(errOut, "no-such-program-on-path") {
		t.Fatalf("a missing program: %d %q", code, errOut)
	}
}

func TestRefuseBatchFile(t *testing.T) {
	for _, tc := range []struct {
		goos, path string
		refused    bool
	}{
		{"windows", `C:\tools\taskr.bat`, true},
		{"windows", `C:\tools\taskr.CMD`, true},
		{"windows", `C:\tools\taskr.exe`, false},
		{"linux", "/usr/bin/taskr.bat", false},
		{"darwin", "/usr/local/bin/taskr", false},
	} {
		err := refuseBatchFile(tc.goos, tc.path)
		if (err != nil) != tc.refused {
			t.Errorf("%s %s: %v, want refused %v", tc.goos, tc.path, err, tc.refused)
		}
		if err != nil && !strings.Contains(err.Error(), "cmd.exe") {
			t.Errorf("%s: the error doesn't say why: %v", tc.path, err)
		}
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
