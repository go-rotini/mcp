package clients

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testDirs(t *testing.T) Dirs {
	t.Helper()
	root := t.TempDir()
	return Dirs{Home: filepath.Join(root, "home"), Config: filepath.Join(root, "config"), Project: filepath.Join(root, "project")}
}

func TestPaths(t *testing.T) {
	d := Dirs{Home: "/h", Config: "/c", Project: "/p"}
	cases := []struct {
		c     Client
		scope Scope
		want  string
	}{
		{ClaudeDesktop, "", "/c/Claude/claude_desktop_config.json"},
		{ClaudeCode, "", "/p/.mcp.json"},
		{Cursor, "", "/h/.cursor/mcp.json"},
		{Cursor, Project, "/p/.cursor/mcp.json"},
		{VSCode, User, "/c/Code/User/mcp.json"},
		{VSCode, Project, "/p/.vscode/mcp.json"},
	}
	for _, tc := range cases {
		got, err := tc.c.Path(tc.scope, d)
		if err != nil || got != filepath.FromSlash(tc.want) {
			t.Errorf("%s %s: %q, %v; want %q", tc.c, tc.scope, got, err, tc.want)
		}
	}
	if _, err := ClaudeDesktop.Path(Project, d); !errors.Is(err, ErrScope) {
		t.Errorf("claude-desktop project: %v, want ErrScope", err)
	}
	if _, err := Client("nope").Path("", d); err == nil {
		t.Error("an unknown client has a path")
	}
}

func TestEnableListDisable(t *testing.T) {
	d := testDirs(t)
	s := Server{Command: "/bin/taskr", Args: []string{"mcp"}, Env: map[string]string{"TASKR_TOKEN": "x"}}
	for _, c := range Clients() {
		t.Run(string(c), func(t *testing.T) {
			path, err := Enable(c, "", d, "taskr", s)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path + ".bak"); !errors.Is(err, os.ErrNotExist) {
				t.Error("a new file was backed up")
			}
			_, servers, err := List(c, "", d)
			if err != nil {
				t.Fatal(err)
			}
			got := servers["taskr"]
			if got.Command != s.Command || strings.Join(got.Args, " ") != "mcp" || got.Env["TASKR_TOKEN"] != "x" {
				t.Fatalf("listed %+v", got)
			}
			b, _ := os.ReadFile(path)
			key := `"mcpServers"`
			if c == VSCode {
				key = `"servers"`
				if !strings.Contains(string(b), `"type": "stdio"`) {
					t.Error("vscode entry has no type")
				}
			}
			if !strings.Contains(string(b), key) {
				t.Errorf("%s: no %s key:\n%s", path, key, b)
			}
			if _, err := Enable(c, "", d, "other", Server{Command: "o"}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path + ".bak"); err != nil {
				t.Errorf("no backup before rewriting: %v", err)
			}
			_, removed, err := Disable(c, "", d, "taskr")
			if err != nil || !removed {
				t.Fatalf("disable: %v %v", removed, err)
			}
			_, servers, _ = List(c, "", d)
			if _, ok := servers["taskr"]; ok || len(servers) != 1 {
				t.Errorf("after disable: %v", servers)
			}
			if _, removed, _ := Disable(c, "", d, "taskr"); removed {
				t.Error("removed a server twice")
			}
		})
	}
}

func TestKeepsOtherKeysAndReadsComments(t *testing.T) {
	d := testDirs(t)
	path, _ := VSCode.Path(Project, d)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := "// workspace servers\n{\n  \"inputs\": [],\n  \"servers\": {\"keep\": {\"command\": \"k\"},},\n}\n"
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Enable(VSCode, Project, d, "taskr", Server{Command: "t"}); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["inputs"]; !ok {
		t.Error("lost the inputs key")
	}
	servers, _ := doc["servers"].(map[string]any)
	if servers["keep"] == nil || servers["taskr"] == nil {
		t.Errorf("servers = %v", servers)
	}
	bak, _ := os.ReadFile(path + ".bak")
	if string(bak) != orig {
		t.Error("the backup isn't the original file")
	}
}

func TestErrors(t *testing.T) {
	d := testDirs(t)
	if _, err := Enable(ClaudeCode, "", d, "", Server{Command: "x"}); err == nil {
		t.Error("enabled a server with no name")
	}
	path, _ := ClaudeCode.Path("", d)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(`{"mcpServers": []}`), 0o600)
	if _, err := Enable(ClaudeCode, "", d, "x", Server{Command: "x"}); err == nil {
		t.Error("accepted mcpServers that isn't an object")
	}
	_ = os.WriteFile(path, []byte(`not json`), 0o600)
	if _, _, err := List(ClaudeCode, "", d); err == nil {
		t.Error("read a file that isn't JSON")
	}
	_ = os.WriteFile(path, nil, 0o600)
	if _, servers, err := List(ClaudeCode, "", d); err != nil || len(servers) != 0 {
		t.Errorf("an empty file: %v %v", servers, err)
	}
}

func TestRewriteKeepsValues(t *testing.T) {
	d := testDirs(t)
	path, _ := ClaudeCode.Path("", d)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := "{\n  // the team's servers\n  \"id\": 12345678901234567890,\n  \"ratio\": 0.1000000000000000055511151231257827,\n" +
		"  \"mcpServers\": {\"keep\": {\"command\": \"k\", \"args\": [\"a&b\", \"<x>\"]}}, /* trailing */\n}\n"
	if err := os.WriteFile(path, []byte(orig), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := Enable(ClaudeCode, "", d, "first", Server{Command: "run&go", Env: map[string]string{"Q": "<&>"}}); err != nil {
		t.Fatal(err)
	}
	afterFirst, _ := os.ReadFile(path)
	if _, err := Enable(ClaudeCode, "", d, "second", Server{Command: "s"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{`"id": 12345678901234567890`, `"ratio": 0.1000000000000000055511151231257827`, `"a&b"`, `"<x>"`, `"run&go"`, `"<&>"`, `"first"`, `"second"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the file lost %s:\n%s", want, b)
		}
	}
	if strings.Contains(string(b), "the team's servers") {
		t.Error("comments are kept; the docs say they aren't")
	}
	if bak, _ := os.ReadFile(path + ".bak"); string(bak) != string(afterFirst) {
		t.Errorf("the backup isn't the file before the last change:\n%s", bak)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want the file's own 0640", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("left a temporary file: %s", e.Name())
		}
	}
}

// A configuration file that is a symbolic link (a dotfiles setup) keeps the link: the file it
// links to is the one rewritten.
func TestEnableThroughSymlink(t *testing.T) {
	d := testDirs(t)
	dotfiles := t.TempDir()
	target := filepath.Join(dotfiles, "cursor-mcp.json")
	if err := os.WriteFile(target, []byte(`{"mcpServers":{}}`), 0o640); err != nil {
		t.Fatal(err)
	}
	link, err := Cursor.Path(User, d)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	if _, err := Enable(Cursor, User, d, "taskr", Server{Command: "taskr"}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced: %v %v", info, err)
	}
	b, _ := os.ReadFile(target)
	if !strings.Contains(string(b), `"taskr"`) {
		t.Fatalf("the linked file wasn't changed:\n%s", b)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(target); info.Mode().Perm() != 0o640 {
			t.Errorf("mode %v, want 0640", info.Mode().Perm())
		}
	}
}

// Enabling a server again keeps the entry's other settings, and the file keeps its key order.
func TestEnableKeepsEntrySettingsAndOrder(t *testing.T) {
	d := testDirs(t)
	path, err := Cursor.Path(Project, d)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := `{
  // my servers
  "zeta": 1,
  "mcpServers": {
    "taskr": {"command": "old", "args": ["x"], "disabled": true, "autoApprove": ["taskr_list"], "cwd": "/srv", "env": {"A": "1"},},
    "b": {"command": "b"},
  },
  "alpha": 2,
}`
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Enable(Cursor, Project, d, "taskr", Server{Command: "taskr", Args: []string{"mcp"}}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	got := string(b)
	for _, want := range []string{`"disabled": true`, `"autoApprove"`, `"cwd": "/srv"`, `"A": "1"`, `"command": "taskr"`, `"mcp"`} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"x"`) {
		t.Errorf("the old args were kept:\n%s", got)
	}
	order := []string{`"zeta"`, `"mcpServers"`, `"taskr"`, `"command"`, `"args"`, `"disabled"`, `"b"`, `"alpha"`}
	last := -1
	for _, k := range order {
		i := strings.Index(got, k)
		if i < last {
			t.Fatalf("%s moved; want the order %v:\n%s", k, order, got)
		}
		last = i
	}
}
