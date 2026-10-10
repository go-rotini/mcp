package mcp

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// taskrTools reads the tools document rotini's generator writes for its taskr fixture (copied
// from rotini's goldens), so a change to the format shows here.
func taskrTools(t *testing.T) map[string]Tool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "taskr.mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	tools, err := ParseTools(b)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]Tool{}
	for _, tl := range tools {
		m[tl.Name] = tl
	}
	return m
}

func jsonArgs(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestArgv_taskr(t *testing.T) {
	tools := taskrTools(t)
	cases := []struct {
		tool, args string
		argv       []string
		stdin      string
	}{
		{"taskr_add", `{"title":"buy milk"}`, []string{"add", "--", "buy milk"}, ""},
		{"taskr_add", `{"title":"-v","tag":["home","x,y"],"priority":"high","due":"2026-10-01","verbose":3}`,
			[]string{"add", "--due=2026-10-01", "--priority=high", "--tag=home", "--tag=x,y", "--verbose", "--verbose", "--verbose", "--", "-v"}, ""},
		{"taskr_list", `{}`, []string{"list", "--format=json"}, ""},
		{"taskr_list", `{"label":{"team":"core","a":"b=c"},"page":"tok"}`,
			[]string{"list", "--format=json", "--label=a=b=c", "--label=team=core", "--page=tok"}, ""},
		{"taskr_purge", `{"dry-run":true,"yes":false}`, []string{"purge", "--dry-run", "--yes=false"}, ""},
		{"taskr_import", `{"stdin":[{"id":1,"title":"a"}]}`, []string{"import"}, `[{"id":1,"title":"a"}]` + "\n"},
		{"taskr_remote_sync", `{"prune":true}`, []string{"remote", "sync", "--prune"}, ""},
		{"taskr_add", `{"title":"x","verbose":0}`, []string{"add", "--", "x"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.tool+" "+tc.args, func(t *testing.T) {
			tl, ok := tools[tc.tool]
			if !ok {
				t.Fatalf("no tool %s", tc.tool)
			}
			argv, stdin, err := tl.Invoke.Argv(jsonArgs(t, tc.args))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(argv, tc.argv) {
				t.Errorf("argv = %q\nwant   %q", argv, tc.argv)
			}
			if string(stdin) != tc.stdin {
				t.Errorf("stdin = %q, want %q", stdin, tc.stdin)
			}
		})
	}
}

func TestArgv_rules(t *testing.T) {
	inv := Invoke{
		Path: []string{"run"},
		Arguments: []Argument{
			{Name: "first", Kind: KindScalar},
			{Name: "rest", Kind: KindList, Variadic: true},
		},
		Flags: map[string]Flag{
			"csv":   {Flag: "--csv", Kind: KindList, Type: "[]string", Separator: ","},
			"in":    {Flag: "--in", Kind: KindScalar, Type: "string", From: []string{"value", "file", "stdin"}},
			"n":     {Flag: "--n", Kind: KindScalar, Type: "int"},
			"meta":  {Flag: "--meta", Kind: KindMap, Type: "map[string]any"},
			"obj":   {Flag: "--obj", Kind: KindObject, Type: "DB"},
			"objs":  {Flag: "--objs", Kind: KindObject, Type: "[]DB"},
			"plain": {Flag: "--plain", Kind: KindList, Type: "[]string"},
			"key":   {Flag: "--key", Kind: KindScalar, Type: "string", Secret: true},
			"argf":  {Flag: "--argf", Kind: KindScalar, Type: "float64"},
			"v":     {Flag: "-v", Kind: KindCount, Type: "count"},
			"level": {Flag: "--level", Kind: KindCount, Type: "count", HasDefault: true},
		},
	}
	ok := []struct {
		args string
		want []string
	}{
		{`{"csv":["a","b,c","say \"hi\""]}`, []string{"run", `--csv=a`, `--csv="b,c"`, `--csv="say ""hi"""`}},
		{`{"csv":[]}`, []string{"run", "--csv="}},
		{`{"in":"@file.txt"}`, []string{"run", "--in=@@file.txt"}},
		{`{"n":3.0}`, []string{"run", "--n=3"}},
		{`{"n":12345678901234567890}`, []string{"run", "--n=12345678901234567890"}},
		{`{"argf":1e21}`, []string{"run", "--argf=1e21"}},
		{`{"meta":{"b":true,"a":2.5,"c":"text","d":null}}`, []string{"run", "--meta=a=2.5", "--meta=b=true", "--meta=c=text", "--meta=d=null"}},
		{`{"obj":{"host":"h","port":5432}}`, []string{"run", `--obj={"host":"h","port":5432}`}},
		{`{"objs":[{"a":1},{"a":"<b>"}]}`, []string{"run", `--objs={"a":1}`, `--objs={"a":"<b>"}`}},
		{`{"first":"--help","rest":["-x","y"]}`, []string{"run", "--", "--help", "-x", "y"}},
		{`{"first":"only","rest":[]}`, []string{"run", "--", "only"}},
		{`{"first":"@x","rest":["-"]}`, []string{"run", "--", "@x", "-"}},
		{`{"v":0}`, []string{"run"}},
		{`{"v":2}`, []string{"run", "-v", "-v"}},
		{`{"level":2}`, []string{"run", "--level", "--level"}},
		{`{"level":null}`, []string{"run"}},
	}
	for _, tc := range ok {
		t.Run(tc.args, func(t *testing.T) {
			argv, _, err := inv.Argv(jsonArgs(t, tc.args))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(argv, tc.want) {
				t.Errorf("argv = %q\nwant   %q", argv, tc.want)
			}
		})
	}
	bad := []struct {
		args, param, reason string
	}{
		{`{"bogus":1}`, "bogus", "no such parameter"},
		{`{"key":"s3cret"}`, "key", "secret"},
		{`{"key":"s3cret"}`, "key", "server's environment"},
		{`{"level":0}`, "level", "default"},
		{`{"v":-1}`, "v", "at least 0"},
		{`{"in":"-"}`, "in", "reads stdin"},
		{`{"rest":["x"]}`, "rest", "left out"},
		{`{"n":2.5}`, "n", "whole number"},
		{`{"meta":{"a":"true"}}`, "meta", "read back"},
		{`{"meta":{"a=b":"c"}}`, "meta", "key"},
		{`{"meta":{"a":{"nested":1}}}`, "meta", "free-form"},
		{`{"plain":[]}`, "plain", "an explicit empty list can't be written on a flag without a separator"},
		{`{"meta":{}}`, "meta", "an explicit empty list can't be written on a flag without a separator"},
		{`{"objs":[]}`, "objs", "without a separator"},
		{`{"csv":["a\rb"]}`, "csv", "carriage return"},
		{`{"csv":"notalist"}`, "csv", "array"},
	}
	for _, tc := range bad {
		t.Run(tc.args, func(t *testing.T) {
			_, _, err := inv.Argv(jsonArgs(t, tc.args))
			var ae *ArgvError
			if !errors.As(err, &ae) || ae.Param != tc.param || !strings.Contains(ae.Reason, tc.reason) {
				t.Fatalf("err = %v, want an ArgvError for %s mentioning %q", err, tc.param, tc.reason)
			}
			if strings.Contains(err.Error(), "s3cret") {
				t.Error("the error echoes the secret")
			}
		})
	}
}

func TestArgv_passthrough(t *testing.T) {
	whole := Invoke{Path: []string{"exec"}, Passthrough: true, Arguments: []Argument{{Name: "argv", Kind: KindList, Variadic: true}}}
	argv, _, err := whole.Argv(jsonArgs(t, `{"argv":["ls","-la","--"]}`))
	if err != nil || !slices.Equal(argv, []string{"exec", "ls", "-la", "--"}) {
		t.Errorf("command passthrough: argv = %q, %v", argv, err)
	}
	// The fixed and inherited flags go before the passthrough command's name, where the parser
	// still reads flags.
	nested := Invoke{
		Path:        []string{"tools", "exec"},
		Passthrough: true,
		Fixed:       []string{"--json"},
		Flags:       map[string]Flag{"verbose": {Flag: "-v", Kind: KindCount, Type: "count"}},
		Arguments:   []Argument{{Name: "argv", Kind: KindList, Variadic: true}},
	}
	argv, _, err = nested.Argv(jsonArgs(t, `{"verbose":2,"argv":["ls","--json"]}`))
	if want := []string{"tools", "--json", "-v", "-v", "exec", "ls", "--json"}; err != nil || !slices.Equal(argv, want) {
		t.Errorf("passthrough flags: argv = %q, %v\nwant   %q", argv, err, want)
	}
	argv, _, err = nested.Argv(jsonArgs(t, `{}`))
	if want := []string{"tools", "--json", "exec"}; err != nil || !slices.Equal(argv, want) {
		t.Errorf("passthrough with no words: argv = %q, %v\nwant   %q", argv, err, want)
	}
	root := Invoke{Path: []string{}, Passthrough: true, Fixed: []string{"--json"}, Arguments: whole.Arguments}
	if _, _, err := root.Argv(jsonArgs(t, `{"argv":["x"]}`)); err == nil {
		t.Error("a passthrough root's fixed flag was written as a raw word")
	}
	root.Fixed = nil
	if argv, _, err := root.Argv(jsonArgs(t, `{"argv":["x"]}`)); err != nil || !slices.Equal(argv, []string{"x"}) {
		t.Errorf("passthrough root: argv = %q, %v", argv, err)
	}
	// With response files on, a raw word starting with the prefix has it doubled up to the
	// first "--"; after it the program reads words as typed.
	resp := whole
	resp.ResponsePrefix = "@"
	argv, _, err = resp.Argv(jsonArgs(t, `{"argv":["@/etc/hosts","@","x@y","--","@after"]}`))
	if want := []string{"exec", "@@/etc/hosts", "@", "x@y", "--", "@after"}; err != nil || !slices.Equal(argv, want) {
		t.Errorf("response prefix: argv = %q, %v\nwant   %q", argv, err, want)
	}
	partial := Invoke{Path: []string{"run"}, Arguments: []Argument{
		{Name: "script", Kind: KindScalar},
		{Name: "args", Kind: KindList, Variadic: true, Passthrough: true},
	}}
	argv, _, err = partial.Argv(jsonArgs(t, `{"script":"s.sh","args":["-v","x"]}`))
	if err != nil || !slices.Equal(argv, []string{"run", "--", "s.sh", "-v", "x"}) {
		t.Errorf("passthrough argument: argv = %q, %v", argv, err)
	}
	only := Invoke{Path: []string{"run"}, Arguments: []Argument{{Name: "args", Kind: KindList, Variadic: true, Passthrough: true}}}
	argv, _, err = only.Argv(jsonArgs(t, `{"args":["-x"]}`))
	if err != nil || !slices.Equal(argv, []string{"run", "--", "-x"}) {
		t.Errorf("first word of a passthrough argument: argv = %q, %v", argv, err)
	}
}

func TestArgv_argumentSeparator(t *testing.T) {
	inv := Invoke{Path: []string{"tag"}, Arguments: []Argument{{Name: "items", Kind: KindList, Variadic: true, Separator: ","}}}
	argv, _, err := inv.Argv(jsonArgs(t, `{"items":["a","b,c","say \"hi\"",""," lead"]}`))
	want := []string{"tag", "--", "a", `"b,c"`, `"say ""hi"""`, `""`, `" lead"`}
	if err != nil || !slices.Equal(argv, want) {
		t.Errorf("argv = %q, %v\nwant   %q", argv, err, want)
	}
	// A separator is taken literally, the NUL character too.
	nul := Invoke{
		Path:      []string{"tag"},
		Arguments: []Argument{{Name: "items", Kind: KindList, Variadic: true, Separator: "\x00"}},
		Flags:     map[string]Flag{"f": {Flag: "--f", Kind: KindList, Type: "[]string", Separator: "\x00"}},
	}
	argv, _, err = nul.Argv(jsonArgs(t, `{"items":["a,b","nul"],"f":["x,y",""]}`))
	want = []string{"tag", "--f=x,y", `--f=""`, "--", "a,b", "nul"}
	if err != nil || !slices.Equal(argv, want) {
		t.Errorf("NUL separator: argv = %q, %v\nwant   %q", argv, err, want)
	}
	_, _, err = inv.Argv(jsonArgs(t, `{"items":["a\rb"]}`))
	if ae, ok := errors.AsType[*ArgvError](err); !ok || ae.Param != "items" || !strings.Contains(ae.Reason, "carriage return") {
		t.Errorf("err = %v, want a carriage-return refusal", err)
	}
}

func TestArgv_dottedKeys(t *testing.T) {
	inv := Invoke{Path: []string{"set"}, Flags: map[string]Flag{
		"set": {Flag: "--set", Kind: KindMap, Type: "map[string]any", DottedKeys: true},
		"csv": {Flag: "--csv", Kind: KindMap, Type: "map[string]any", DottedKeys: true, Separator: ","},
	}}
	argv, _, err := inv.Argv(jsonArgs(t, `{"set":{"db":{"host":"h","port":5432},"debug":true,"x":null}}`))
	want := []string{"set", "--set=db.host=h", "--set=db.port=5432", "--set=debug=true", "--set=x=null"}
	if err != nil || !slices.Equal(argv, want) {
		t.Errorf("argv = %q, %v\nwant   %q", argv, err, want)
	}
	argv, _, err = inv.Argv(jsonArgs(t, `{"csv":{"a":{"b":"x,y"}}}`))
	if want := []string{"set", `--csv="a.b=x,y"`}; err != nil || !slices.Equal(argv, want) {
		t.Errorf("argv = %q, %v\nwant   %q", argv, err, want)
	}
	for _, tc := range []struct{ args, reason string }{
		{`{"set":{"a.b":"x"}}`, `hold no "." or "="`},
		{`{"set":{"a":{"b=c":"x"}}}`, `hold no "." or "="`},
		{`{"set":{"a":{"":"x"}}}`, "aren't empty"},
		{`{"set":{"a":{}}}`, `the empty map under "a"`},
		{`{"set":{"a":{"b":"true"}}}`, "read back"},
		{`{"set":{"a":[1]}}`, "free-form"},
		{`{"set":{}}`, "without a separator"},
	} {
		_, _, err := inv.Argv(jsonArgs(t, tc.args))
		if ae, ok := errors.AsType[*ArgvError](err); !ok || ae.Param != "set" || !strings.Contains(ae.Reason, tc.reason) {
			t.Errorf("%s: err = %v, want an ArgvError mentioning %q", tc.args, err, tc.reason)
		}
	}
}

func TestArgv_fromFile(t *testing.T) {
	inv := Invoke{
		Path: []string{"cat"},
		Arguments: []Argument{
			{Name: "src", Kind: KindScalar, From: []string{"value", "file"}},
			{Name: "rest", Kind: KindList, Variadic: true, From: []string{"stdin"}},
		},
		Flags: map[string]Flag{
			"tag":  {Flag: "--tag", Kind: KindList, Type: "[]string", Separator: ",", From: []string{"file"}},
			"name": {Flag: "--name", Kind: KindScalar, Type: "string"},
		},
	}
	argv, _, err := inv.Argv(jsonArgs(t, `{"src":"@a","rest":["@b"],"tag":["@c","d"],"name":"@e"}`))
	want := []string{"cat", "--name=@e", "--tag=@@c", "--tag=d", "--", "@@a", "@b"}
	if err != nil || !slices.Equal(argv, want) {
		t.Errorf("argv = %q, %v\nwant   %q", argv, err, want)
	}
	if _, _, err := inv.Argv(jsonArgs(t, `{"src":"x","rest":["-"]}`)); err == nil {
		t.Error(`"-" on an argument that reads stdin was written`)
	}
	if argv, _, err := inv.Argv(jsonArgs(t, `{"src":"-"}`)); err != nil || !slices.Equal(argv, []string{"cat", "--", "-"}) {
		t.Errorf(`"-" without from stdin: argv = %q, %v`, argv, err)
	}
}

func TestArgv_secretArgument(t *testing.T) {
	inv := Invoke{Path: []string{"login"}, Arguments: []Argument{{Name: "token", Kind: KindScalar, Secret: true}}}
	_, _, err := inv.Argv(jsonArgs(t, `{"token":"s3cret"}`))
	ae, ok := errors.AsType[*ArgvError](err)
	if !ok || ae.Param != "token" || !strings.Contains(ae.Reason, "environment") || strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("err = %v, want a refusal pointing at the environment", err)
	}
}

func TestArgv_stdinFormats(t *testing.T) {
	for _, tc := range []struct {
		format, args, want string
		err                bool
	}{
		{"json", `{"stdin":{"a":1}}`, "{\"a\":1}\n", false},
		{"yaml", `{"stdin":[1,2]}`, "[1,2]\n", false},
		{"lines", `{"stdin":"a\nb\n"}`, "a\nb\n", false},
		{"text", `{"stdin":5}`, "", true},
	} {
		inv := Invoke{Path: []string{}, Stdin: &Stdin{Param: "stdin", Format: tc.format}}
		_, stdin, err := inv.Argv(jsonArgs(t, tc.args))
		if (err != nil) != tc.err || string(stdin) != tc.want {
			t.Errorf("%s: stdin = %q, err = %v", tc.format, stdin, err)
		}
	}
}

func FuzzArgv(f *testing.F) {
	f.Add("value", "-x", "@f")
	f.Add("", "--", "-")
	f.Add("a,b", "\"q\"", " lead")
	f.Add("@@x", "@y", "@z")
	inv := Invoke{
		Path:      []string{"cmd"},
		Arguments: []Argument{{Name: "pos", Kind: KindScalar}},
		Flags: map[string]Flag{
			"s":    {Flag: "--s", Kind: KindScalar, Type: "string", From: []string{"value", "file"}},
			"list": {Flag: "--list", Kind: KindList, Type: "[]string", Separator: ","},
		},
	}
	f.Fuzz(func(t *testing.T, s, pos, item string) {
		argv, _, err := inv.Argv(map[string]any{"s": s, "pos": pos, "list": []any{item}})
		if err != nil {
			if _, ok := errors.AsType[*ArgvError](err); !ok {
				t.Fatalf("not an ArgvError: %v", err)
			}
			return
		}
		dash := slices.Index(argv, "--")
		if dash < 0 || dash != len(argv)-2 || argv[len(argv)-1] != pos {
			t.Fatalf("the positional isn't alone after --: %q", argv)
		}
		for _, w := range argv[1:dash] {
			if !strings.HasPrefix(w, "--s=") && !strings.HasPrefix(w, "--list=") {
				t.Fatalf("a flag word without its flag: %q", w)
			}
		}
		for _, w := range argv[1:dash] {
			if v, ok := strings.CutPrefix(w, "--s="); ok && strings.HasPrefix(s, "@") && v != "@"+s {
				t.Fatalf("an @ value on a from:[file] flag isn't escaped: %q", w)
			}
		}
	})
}
