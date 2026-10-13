package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/big"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/go-rotini/rotini"
)

// roundTripInputs is what the round-trip program's command reads.
type roundTripInputs struct {
	App struct {
		Flags struct {
			S    string            `rotini:"s"`
			List []string          `rotini:"list"`
			Tag  []string          `rotini:"tag"`
			M    map[string]string `rotini:"m"`
			On   bool              `rotini:"on"`
		}
		Arguments struct {
			Pos  string   `rotini:"pos"`
			Rest []string `rotini:"rest"`
		}
	}
}

type roundTripHandlers struct{ got *roundTripInputs }

func (h roundTripHandlers) App() rotini.Handler { return roundTripRun{h: h} }

type roundTripRun struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun

	h roundTripHandlers
}

func (r roundTripRun) Run(_ context.Context, rtx *rotini.Context) {
	in, err := rtx.Inputs[roundTripInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	*r.h.got = in
}

// roundTripDef is the command the round-trip facts describe, as rotini parses it.
func roundTripDef() rotini.Definition {
	return rotini.Definition{
		Name: "app", Handler: "App", Inputs: reflect.TypeFor[roundTripInputs](),
		Flags: []rotini.FlagDef{
			{Name: "s", Identifiers: []string{"--s"}, Type: "string", From: []string{"value", "file"}},
			{Name: "list", Identifiers: []string{"--list"}, Type: "[]string", Separator: ","},
			{Name: "tag", Identifiers: []string{"--tag"}, Type: "[]string"},
			{Name: "m", Identifiers: []string{"--m"}, Type: "map[string]string"},
			{Name: "on", Identifiers: []string{"--on"}, Type: "bool", Negatable: true},
		},
		Arguments: []rotini.ArgDef{
			{Name: "pos", Type: "string"},
			{Name: "rest", Type: "[]string", Variadic: true},
		},
	}
}

var roundTripInvoke = Invoke{
	Path: []string{},
	Arguments: []Argument{
		{Name: "pos", Kind: KindScalar},
		{Name: "rest", Kind: KindList, Variadic: true},
	},
	Flags: map[string]Flag{
		"s":    {Flag: "--s", Kind: KindScalar, Type: "string", From: []string{"value", "file"}},
		"list": {Flag: "--list", Kind: KindList, Type: "[]string", Separator: ","},
		"tag":  {Flag: "--tag", Kind: KindList, Type: "[]string"},
		"m":    {Flag: "--m", Kind: KindMap, Type: "map[string]string"},
		"on":   {Flag: "--on", Kind: KindScalar, Type: "bool", Negated: "--no-on"},
	},
}

// parseRoundTrip parses argv with rotini's own parser and returns the values it read.
func parseRoundTrip(t *testing.T, argv []string) (roundTripInputs, string) {
	t.Helper()
	var got roundTripInputs
	var stderr strings.Builder
	code, err := rotini.NewProgram(roundTripDef(), roundTripHandlers{got: &got}).
		WithoutSignalHandling().
		WithStdin(nil).
		WithStdout(io.Discard).
		WithStderr(&stderr).
		WithExit(func(int) {}).
		Run(argv)
	if code != 0 || err != nil {
		return got, fmt.Sprintf("exit %d, %v: %s", code, err, stderr.String())
	}
	return got, ""
}

// FuzzArgvRoundTrip builds a command line with Argv, parses it with rotini, and checks that every
// value reads back as given.
func FuzzArgvRoundTrip(f *testing.F) {
	f.Add("value", "-x", "@f", "a,b", "k=v", "=", true)
	f.Add("", "--", "-", "", "", "x", false)
	f.Add("@@x", "@y", " lead", `"q"`, "a.b", "c=d", true)
	f.Fuzz(func(t *testing.T, s, pos, rest, item, key, val string, on bool) {
		args := map[string]any{
			"s": s, "pos": pos, "rest": []any{rest, "-r"}, "list": []any{item, "z"},
			"tag": []any{item}, "m": map[string]any{key: val}, "on": on,
		}
		argv, _, err := roundTripInvoke.Argv(args)
		if err != nil {
			if _, ok := errors.AsType[*ArgvError](err); !ok {
				t.Fatalf("not an ArgvError: %v", err)
			}
			return
		}
		got, failed := parseRoundTrip(t, argv)
		if failed != "" {
			t.Fatalf("rotini didn't parse %q: %s", argv, failed)
		}
		f, a := got.App.Flags, got.App.Arguments
		switch {
		case f.S != s:
			t.Fatalf("s: %q, want %q (argv %q)", f.S, s, argv)
		case a.Pos != pos:
			t.Fatalf("pos: %q, want %q (argv %q)", a.Pos, pos, argv)
		case !slices.Equal(a.Rest, []string{rest, "-r"}):
			t.Fatalf("rest: %q (argv %q)", a.Rest, argv)
		case !slices.Equal(f.List, []string{item, "z"}):
			t.Fatalf("list: %q, want [%q z] (argv %q)", f.List, item, argv)
		case !slices.Equal(f.Tag, []string{item}):
			t.Fatalf("tag: %q (argv %q)", f.Tag, argv)
		case !maps.Equal(f.M, map[string]string{key: val}):
			t.Fatalf("m: %q, want %q=%q (argv %q)", f.M, key, val, argv)
		case f.On != on:
			t.Fatalf("on: %v (argv %q)", f.On, argv)
		}
	})
}

// roundTripDB is the object the kinds program's --db flag reads.
type roundTripDB struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

const roundTripDBSchema = `{"type":"object","properties":{"host":{"type":"string"},"port":{"type":"integer"}}}`

// kindsInputs is what the kinds program's commands read.
type kindsInputs struct {
	Rich struct {
		Flags struct {
			N  int            `rotini:"n"`
			V  int            `rotini:"v"`
			DM map[string]any `rotini:"dm"`
			DB roundTripDB    `rotini:"db"`
		}
		Arguments struct {
			Word string `rotini:"word"`
		}
	}
	Sh struct {
		Arguments struct {
			Words []string `rotini:"words"`
		}
	}
}

type kindsHandlers struct{ got *kindsInputs }

func (kindsHandlers) App() rotini.Handler       { return kindsRun{} }
func (h kindsHandlers) AppRich() rotini.Handler { return kindsRun{got: h.got, rich: true} }
func (h kindsHandlers) AppSh() rotini.Handler   { return kindsRun{got: h.got} }

type kindsRun struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun

	got  *kindsInputs
	rich bool
}

type kindsRichInputs struct {
	Rich struct {
		Flags struct {
			N  int            `rotini:"n"`
			V  int            `rotini:"v"`
			DM map[string]any `rotini:"dm"`
			DB roundTripDB    `rotini:"db"`
		}
		Arguments struct {
			Word string `rotini:"word"`
		}
	}
}

type kindsShInputs struct {
	Sh struct {
		Arguments struct {
			Words []string `rotini:"words"`
		}
	}
}

func (r kindsRun) Run(_ context.Context, rtx *rotini.Context) {
	if r.got == nil {
		return
	}
	if r.rich {
		in, err := rtx.Inputs[kindsRichInputs]()
		if err != nil {
			rtx.HaltWith(err)
			return
		}
		r.got.Rich = in.Rich
		return
	}
	in, err := rtx.Inputs[kindsShInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	r.got.Sh = in.Sh
}

// kindsDef is a program with an int flag, a count, a dotted-keys map, an object flag and a
// passthrough command, reading response files.
func kindsDef() rotini.Definition {
	return rotini.Definition{
		Name: "app", Handler: "App",
		ResponseFiles: &rotini.ResponseFilesDef{Prefix: "@"},
		Commands: []rotini.CommandDef{
			{
				Name: "rich", Handler: "AppRich", Inputs: reflect.TypeFor[kindsRichInputs](),
				Flags: []rotini.FlagDef{
					{Name: "n", Identifiers: []string{"--n"}, Type: "int"},
					{Name: "v", Identifiers: []string{"-v"}, Type: "count"},
					{Name: "dm", Identifiers: []string{"--dm"}, Type: "map[string]any", DottedKeys: true},
					{Name: "db", Identifiers: []string{"--db"}, Type: "roundTripDB", ObjectSchema: roundTripDBSchema},
				},
				Arguments: []rotini.ArgDef{{Name: "word", Type: "string"}},
			},
			{
				Name: "sh", Handler: "AppSh", Passthrough: true, Inputs: reflect.TypeFor[kindsShInputs](),
				Arguments: []rotini.ArgDef{{Name: "words", Type: "[]string", Variadic: true}},
			},
		},
	}
}

var (
	kindsRichInvoke = Invoke{
		Path:           []string{"rich"},
		ResponsePrefix: "@",
		Arguments:      []Argument{{Name: "word", Kind: KindScalar}},
		Flags: map[string]Flag{
			"n":  {Flag: "--n", Kind: KindScalar, Type: "int"},
			"v":  {Flag: "-v", Kind: KindCount, Type: "count"},
			"dm": {Flag: "--dm", Kind: KindMap, Type: "map[string]any", DottedKeys: true},
			"db": {Flag: "--db", Kind: KindObject, Type: "roundTripDB"},
		},
	}
	kindsShInvoke = Invoke{
		Path:           []string{"sh"},
		Passthrough:    true,
		ResponsePrefix: "@",
		Arguments:      []Argument{{Name: "words", Kind: KindList, Variadic: true}},
	}
)

func parseKinds(t *testing.T, argv []string) (kindsInputs, string) {
	t.Helper()
	var got kindsInputs
	var stderr strings.Builder
	code, err := rotini.NewProgram(kindsDef(), kindsHandlers{got: &got}).
		WithoutSignalHandling().
		WithStdin(nil).
		WithStdout(io.Discard).
		WithStderr(&stderr).
		WithExit(func(int) {}).
		Run(argv)
	if code != 0 || err != nil {
		return got, fmt.Sprintf("exit %d, %v: %s", code, err, stderr.String())
	}
	return got, ""
}

// FuzzArgvRoundTripKinds is [FuzzArgvRoundTrip] for an int flag given in any JSON number form,
// a count, a dotted-keys map, an object flag, and a passthrough command whose words may start
// with the response-file prefix.
func FuzzArgvRoundTripKinds(f *testing.F) {
	f.Add("1e3", uint16(2), "a", "b", "v", "h", int64(5432), "@x", "--y")
	f.Add("9223372036854775807", uint16(0), "k.k", "x", "1", "", int64(-1), "@@x", "-")
	f.Add("9223372036854775808", uint16(65535), "", "=", "true", "@h", int64(0), "", "@")
	f.Add("-1e18", uint16(1), " ", "a b", "x=y", "h\"", int64(1<<40), "a\nb", "--")
	f.Add("1e5000000", uint16(3), "a", "b", "c", "d", int64(1), "w", "x")
	f.Add("1"+strings.Repeat("0", 100000), uint16(4), "a", "b", "c", "d", int64(2), "w", "x")
	f.Fuzz(func(t *testing.T, n string, v uint16, k1, k2, val, host string, port int64, word, rest string) {
		for _, s := range []string{n, k1, k2, val, host, word, rest} {
			if !utf8.ValidString(s) {
				return // a tool call's arguments are JSON, which holds only valid UTF-8
			}
		}
		args := map[string]any{
			"n": json.Number(n), "v": json.Number(strconv.Itoa(int(v))), "word": word,
			"dm": map[string]any{k1: map[string]any{k2: val}},
			"db": map[string]any{"host": host, "port": json.Number(strconv.FormatInt(port, 10))},
		}
		if argv, _, err := kindsRichInvoke.Argv(args); err == nil {
			got, failed := parseKinds(t, argv)
			want, _ := new(big.Int).SetString(argvValue(argv, "--n="), 10)
			switch {
			case failed != "" && want != nil && !want.IsInt64():
				// rotini's int refuses what Argv lets through for a wider integer type
			case failed != "":
				t.Fatalf("rotini didn't parse %q: %s", argv, failed)
			case want == nil || int64(got.Rich.Flags.N) != want.Int64():
				t.Fatalf("n: %d, want %s (argv %q)", got.Rich.Flags.N, n, argv)
			case got.Rich.Flags.V != int(v):
				t.Fatalf("v: %d, want %d (argv %q)", got.Rich.Flags.V, v, argv)
			case !reflect.DeepEqual(got.Rich.Flags.DM, map[string]any{k1: map[string]any{k2: val}}):
				t.Fatalf("dm: %#v (argv %q)", got.Rich.Flags.DM, argv)
			case got.Rich.Flags.DB != roundTripDB{Host: host, Port: int(port)}:
				t.Fatalf("db: %+v (argv %q)", got.Rich.Flags.DB, argv)
			case got.Rich.Arguments.Word != word:
				t.Fatalf("word: %q, want %q (argv %q)", got.Rich.Arguments.Word, word, argv)
			}
		} else if _, ok := errors.AsType[*ArgvError](err); !ok {
			t.Fatalf("not an ArgvError: %v", err)
		}
		words := []any{word, rest}
		argv, _, err := kindsShInvoke.Argv(map[string]any{"words": words})
		if err != nil {
			if _, ok := errors.AsType[*ArgvError](err); !ok {
				t.Fatalf("not an ArgvError: %v", err)
			}
			return
		}
		got, failed := parseKinds(t, argv)
		if failed != "" {
			t.Fatalf("rotini didn't parse %q: %s", argv, failed)
		}
		if !slices.Equal(got.Sh.Arguments.Words, []string{word, rest}) {
			t.Fatalf("words: %q, want [%q %q] (argv %q)", got.Sh.Arguments.Words, word, rest, argv)
		}
	})
}

// argvValue is the value of the first word of argv that starts with prefix, or "".
func argvValue(argv []string, prefix string) string {
	for _, w := range argv {
		if v, ok := strings.CutPrefix(w, prefix); ok {
			return v
		}
	}
	return ""
}
