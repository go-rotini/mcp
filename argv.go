package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ArgvError is a tool call no command line can express: a parameter the tool doesn't have, a
// value its flag can't take, or a secret.
type ArgvError struct {
	Param  string // the parameter, or "" when the problem isn't one parameter's
	Reason string
}

// Error returns "mcp: <param>: <reason>", leaving out an empty Param.
func (e *ArgvError) Error() string {
	if e.Param == "" {
		return "mcp: " + e.Reason
	}
	return fmt.Sprintf("mcp: %s: %s", e.Param, e.Reason)
}

// Argv turns a tool call's arguments back into the command line that supplies them, the way
// [github.com/go-rotini/rotini.ArgvOf] does for a typed inputs value: argv (without the program
// name) and the bytes to write on stdin.
//
// The command's path comes first, then the fixed flags (the machine-output flag), then one
// attached occurrence per flag value (--name=value), in parameter-name order. A list or map
// repeats the flag (key=value for a map, with nested keys flattened to a.b=value on a flag
// that takes dotted keys), a separator-split one quotes items that hold the separator, an
// object is JSON, a count repeats the bare flag (0 writes nothing), and a bool is its bare flag
// for true and its negated form (or --name=false) for false. A "--" comes before the
// positionals, so a value that looks like a flag stays a positional, also when the last
// argument is a passthrough one; a list argument with a separator quotes each item on its own.
// On a parameter whose `from` includes file, a value starting with @ is written with the @
// doubled. A null or missing parameter is left out, as is an empty list for an argument.
//
// A passthrough command reads every word after its name as typed, so its flags (all inherited:
// a passthrough command declares none) and the fixed flags come before its name, and its words
// follow it with no "--". When the program reads response files, a word before any "--" that
// starts with the prefix has it doubled, so the program doesn't read it as a file.
//
// A call no command line can express is an [*ArgvError]: an unknown parameter, a secret (a
// command line is visible to other users of the machine; the program reads secrets from the
// server's environment), a count of 0 on a count flag with a default, an explicit empty list or
// map on a flag without a separator, "-" on a parameter that reads stdin from it, an argument
// given after one left out, any flag on a passthrough root command, and map keys or free-form
// values that don't read back as given, and a NUL byte in any value but stdin's.
func (inv Invoke) Argv(args map[string]any) (argv []string, stdin []byte, err error) {
	for name := range args {
		if _, ok := inv.Flags[name]; ok {
			continue
		}
		if inv.Stdin != nil && name == inv.Stdin.Param {
			continue
		}
		if slices.ContainsFunc(inv.Arguments, func(a Argument) bool { return a.Name == name }) {
			continue
		}
		return nil, nil, &ArgvError{Param: name, Reason: "the tool has no such parameter"}
	}
	for _, name := range slices.Sorted(maps.Keys(args)) {
		if (inv.Stdin == nil || name != inv.Stdin.Param) && holdsNUL(args[name]) {
			return nil, nil, &ArgvError{Param: name, Reason: "the value holds a NUL byte, which a command line can't carry"}
		}
	}
	flags := slices.Clone(inv.Fixed)
	for _, name := range slices.Sorted(maps.Keys(inv.Flags)) {
		v, ok := args[name]
		if !ok || v == nil {
			continue
		}
		words, err := flagWords(name, inv.Flags[name], v)
		if err != nil {
			return nil, nil, err
		}
		flags = append(flags, words...)
	}
	if inv.Stdin != nil {
		if v, ok := args[inv.Stdin.Param]; ok && v != nil {
			if stdin, err = stdinBytes(inv.Stdin, v); err != nil {
				return nil, nil, err
			}
		}
	}
	words, err := inv.positionals(args)
	if err != nil {
		return nil, nil, err
	}
	if !inv.Passthrough {
		argv = append(slices.Clone(inv.Path), flags...)
		if len(words) > 0 {
			argv = append(append(argv, "--"), words...)
		}
		return argv, stdin, nil
	}
	n := len(inv.Path)
	if n == 0 && len(flags) > 0 {
		return nil, nil, &ArgvError{Reason: "the tool's command is a passthrough root, which reads every word as an argument, so its flags can't be written"}
	}
	argv = slices.Clone(inv.Path[:max(n-1, 0)])
	argv = append(argv, flags...)
	argv = append(argv, inv.Path[max(n-1, 0):]...)
	return append(argv, escapeResponseWords(words, inv.ResponsePrefix)...), stdin, nil
}

// holdsNUL reports whether a JSON value holds a NUL byte in any string or object key.
func holdsNUL(v any) bool {
	switch t := v.(type) {
	case string:
		return strings.IndexByte(t, 0) >= 0
	case []any:
		return slices.ContainsFunc(t, holdsNUL)
	case map[string]any:
		for k, e := range t {
			if strings.IndexByte(k, 0) >= 0 || holdsNUL(e) {
				return true
			}
		}
	}
	return false
}

// escapeResponseWords doubles the response-file prefix on the raw words before the first "--",
// which a run would otherwise read as response files.
func escapeResponseWords(words []string, prefix string) []string {
	if prefix == "" {
		return words
	}
	out := slices.Clone(words)
	for i, w := range out {
		if w == "--" {
			break
		}
		if len(w) > len(prefix) && strings.HasPrefix(w, prefix) {
			out[i] = prefix + w
		}
	}
	return out
}

// positionals spells the arguments in order; each one given must follow only given ones.
func (inv Invoke) positionals(args map[string]any) ([]string, error) {
	var words []string
	missing := ""
	for _, a := range inv.Arguments {
		v, ok := args[a.Name]
		if list, isList := v.([]any); !ok || v == nil || (isList && len(list) == 0) {
			if missing == "" {
				missing = a.Name
			}
			continue
		}
		if missing != "" {
			return nil, &ArgvError{Param: a.Name, Reason: fmt.Sprintf("an argument can't be given while %q before it is left out", missing)}
		}
		if err := refuseSecret(a.Name, a.Secret); err != nil {
			return nil, err
		}
		got, err := argumentWords(a, v)
		if err != nil {
			return nil, err
		}
		words = append(words, got...)
	}
	return words, nil
}

func argumentWords(a Argument, v any) ([]string, error) {
	values := []any{v}
	if list, ok := v.([]any); ok && a.Kind != KindObject {
		values = list
	}
	words := make([]string, 0, len(values))
	for _, item := range values {
		var s string
		var err error
		if a.Kind == KindObject {
			s, err = compactJSON(item)
		} else {
			s, err = scalarText(item, "")
		}
		if err == nil && a.Separator != "" && a.Kind == KindList {
			s, err = csvItem(s, a.Separator)
		}
		if err != nil {
			return nil, &ArgvError{Param: a.Name, Reason: err.Error()}
		}
		if s, err = escapeMarkers(s, a.From); err != nil {
			return nil, &ArgvError{Param: a.Name, Reason: err.Error()}
		}
		words = append(words, s)
	}
	return words, nil
}

// flagWords spells one flag parameter's value.
func flagWords(name string, f Flag, v any) ([]string, error) {
	if err := refuseSecret(name, f.Secret); err != nil {
		return nil, err
	}
	fail := func(err error) ([]string, error) { return nil, &ArgvError{Param: name, Reason: err.Error()} }
	var items []string
	switch f.Kind {
	case KindCount:
		n, err := countValue(v)
		if err != nil {
			return fail(err)
		}
		if n == 0 && f.HasDefault {
			return fail(errors.New("a count of 0 can't be written over the flag's default, since a count flag takes no value; leave the parameter out for the default"))
		}
		return slices.Repeat([]string{f.Flag}, n), nil
	case KindList:
		list, ok := v.([]any)
		if !ok {
			return fail(errors.New("a list parameter takes an array"))
		}
		for _, item := range list {
			s, err := scalarText(item, elemType(f.Type))
			if err != nil {
				return fail(err)
			}
			items = append(items, s)
		}
	case KindMap:
		m, ok := v.(map[string]any)
		if !ok {
			return fail(errors.New("a map parameter takes an object"))
		}
		pairs, err := mapPairs(m, f.Type)
		if f.DottedKeys {
			pairs, err = dottedPairs(m)
		}
		if err != nil {
			return fail(err)
		}
		items = pairs
	case KindObject:
		values := []any{v}
		if list, ok := v.([]any); ok {
			values = list
		}
		for _, item := range values {
			s, err := compactJSON(item)
			if err != nil {
				return fail(err)
			}
			items = append(items, s)
		}
	default:
		if b, ok := v.(bool); ok && (f.Type == "bool" || f.Type == "") {
			switch {
			case b:
				return []string{f.Flag}, nil
			case f.Negated != "":
				return []string{f.Negated}, nil
			}
			return []string{f.Flag + "=false"}, nil
		}
		s, err := scalarText(v, f.Type)
		if err != nil {
			return fail(err)
		}
		items = []string{s}
	}
	return occurrences(name, f, items, f.Kind != KindScalar)
}

// occurrences writes one attached occurrence per item. An empty list or map is "--name=" on a
// flag with a separator, and has no spelling on one without: leaving the flag out would bind
// its default.
func occurrences(name string, f Flag, items []string, split bool) ([]string, error) {
	if len(items) == 0 {
		if f.Separator == "" {
			return nil, &ArgvError{Param: name, Reason: "an explicit empty list can't be written on a flag without a separator"}
		}
		return []string{f.Flag + "="}, nil
	}
	words := make([]string, 0, len(items))
	for _, item := range items {
		if split && f.Separator != "" {
			q, err := csvItem(item, f.Separator)
			if err != nil {
				return nil, &ArgvError{Param: name, Reason: err.Error()}
			}
			item = q
		}
		item, err := escapeMarkers(item, f.From)
		if err != nil {
			return nil, &ArgvError{Param: name, Reason: err.Error()}
		}
		words = append(words, f.Flag+"="+item)
	}
	return words, nil
}

// refuseSecret refuses a secret parameter: argv is visible to other users of the machine. A
// server supplies secrets through its environment.
func refuseSecret(name string, secret bool) error {
	if !secret {
		return nil
	}
	return &ArgvError{Param: name, Reason: "a secret isn't passed on the command line, where other users of the machine can read it; leave the parameter out and set the program's environment variable for it in the server's environment, which each run gets"}
}

// escapeMarkers escapes a value its parameter's `from:` modes would read as a file (a leading
// @, doubled) and refuses the "-" that would read stdin.
func escapeMarkers(s string, from []string) (string, error) {
	if strings.HasPrefix(s, "@") && slices.Contains(from, "file") {
		return "@" + s, nil
	}
	if s == "-" && slices.Contains(from, "stdin") {
		return "", errors.New(`"-" on this parameter reads stdin, and has no other spelling`)
	}
	return s, nil
}

// csvItem quotes a separated list's item when it holds the separator, a quote, a newline or
// leading space, or is empty, as the parser's CSV reading expects.
func csvItem(item, sep string) (string, error) {
	if strings.Contains(item, "\r") {
		return "", fmt.Errorf("the item %q holds a carriage return, which a separated list can't", item)
	}
	if item == "" || strings.Contains(item, sep) || strings.ContainsAny(item, "\"\n") ||
		strings.TrimLeftFunc(item, unicode.IsSpace) != item {
		return `"` + strings.ReplaceAll(item, `"`, `""`) + `"`, nil
	}
	return item, nil
}

// mapPairs spells a map as key=value pairs, sorted by key. A free-form map's value is written
// so the parser types it back as given.
func mapPairs(m map[string]any, typ string) ([]string, error) {
	freeForm := typ == "" || strings.HasSuffix(typ, "]any") || typ == "map" || typ == "object"
	pairs := make([]string, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if strings.TrimSpace(k) == "" || strings.Contains(k, "=") {
			return nil, fmt.Errorf("the key %q can't be written; keys must be non-empty, not only spaces, and hold no \"=\"", k)
		}
		var s string
		var err error
		if freeForm {
			s, err = anyText(k, m[k])
		} else {
			s, err = scalarText(m[k], mapValueType(typ))
		}
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, k+"="+s)
	}
	return pairs, nil
}

// dottedPairs spells a map whose nested keys the flag reads as dotted paths: one a.b=value
// pair per leaf, sorted.
func dottedPairs(m map[string]any) ([]string, error) {
	var pairs []string
	var walk func(prefix string, m map[string]any) error
	walk = func(prefix string, m map[string]any) error {
		for k, v := range m {
			if strings.TrimSpace(k) == "" || strings.ContainsAny(k, ".=") {
				return fmt.Errorf("the key %q can't be written; dotted keys hold no \".\" or \"=\" and aren't empty or only spaces", k)
			}
			if sub, ok := v.(map[string]any); ok {
				if len(sub) == 0 {
					return fmt.Errorf("the empty map under %q can't be written", prefix+k)
				}
				if err := walk(prefix+k+".", sub); err != nil {
					return err
				}
				continue
			}
			s, err := anyText(prefix+k, v)
			if err != nil {
				return err
			}
			pairs = append(pairs, prefix+k+"="+s)
		}
		return nil
	}
	if err := walk("", m); err != nil {
		return nil, err
	}
	slices.Sort(pairs)
	return pairs, nil
}

// anyText spells a free-form map value so the parser's scalar inference reads back the same
// value.
func anyText(key string, v any) (string, error) {
	switch v := v.(type) {
	case nil:
		return "null", nil
	case bool:
		return strconv.FormatBool(v), nil
	case string:
		if inferredAsOther(v) {
			return "", fmt.Errorf("the value %q of %q would read back as a number, a bool or null, not text", v, key)
		}
		return v, nil
	case json.Number, float64, int, int64:
		return scalarText(v, "")
	}
	return "", fmt.Errorf("the value of %q is a %s; a free-form map holds only text, numbers, bools and null", key, jsonKind(v))
}

// inferredAsOther reports whether the parser would read text as something other than a string.
func inferredAsOther(s string) bool {
	switch s {
	case "true", "false", "null":
		return true
	}
	if s == "" || !strings.ContainsAny(s[:1], "-0123456789") || strings.TrimSpace(s) != s {
		return false
	}
	var n float64
	return json.Unmarshal([]byte(s), &n) == nil
}

// scalarText spells one JSON value as command-line text. typ is the parameter's rotini type;
// an integer type writes an integral number like 3.0 as 3.
func scalarText(v any, typ string) (string, error) {
	switch v := v.(type) {
	case string:
		return v, nil
	case bool:
		return strconv.FormatBool(v), nil
	case json.Number:
		if isIntType(typ) {
			return integerText(string(v))
		}
		return string(v), nil
	case float64:
		s := strconv.FormatFloat(v, 'f', -1, 64)
		if isIntType(typ) {
			return integerText(s)
		}
		return s, nil
	case int:
		return strconv.Itoa(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case nil:
		return "", errors.New("null can't be written as a value")
	}
	return "", fmt.Errorf("a %s can't be written as one value", jsonKind(v))
}

// integerText writes an integral number without a fraction or exponent. rotini's integer
// types hold at most 64 bits, so a number outside the int64 and uint64 ranges is refused. The
// text is read digit by digit, never parsed in full, so neither "1e5000000" nor a million
// digits written out costs more than reading them.
func integerText(s string) (string, error) {
	neg, digits, exp, ok := decimalParts(s)
	switch {
	case !ok:
		return "", fmt.Errorf("%q isn't a number", shorten(s))
	case digits == "":
		return "0", nil
	case exp > 20:
		return "", fmt.Errorf("%s is out of range for a 64-bit integer", shorten(s))
	case int64(len(digits)) > exp:
		return "", fmt.Errorf("%s isn't a whole number", shorten(s))
	}
	text := digits + strings.Repeat("0", int(exp)-len(digits))
	var err error
	if neg {
		text = "-" + text
		_, err = strconv.ParseInt(text, 10, 64)
	} else {
		_, err = strconv.ParseUint(text, 10, 64)
	}
	if err != nil {
		return "", fmt.Errorf("%s is out of range for a 64-bit integer", shorten(s))
	}
	return text, nil
}

// decimalParts reads a JSON number as its sign and the value 0.digits × 10^exp, digits having
// no leading or trailing zeros ("" for zero). ok is false when s isn't a JSON number.
func decimalParts(s string) (neg bool, digits string, exp int64, ok bool) {
	i := 0
	run := func() string {
		start := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		return s[start:i]
	}
	if i < len(s) && s[i] == '-' {
		neg = true
		i++
	}
	whole := run()
	if whole == "" {
		return false, "", 0, false
	}
	var frac string
	if i < len(s) && s[i] == '.' {
		i++
		if frac = run(); frac == "" {
			return false, "", 0, false
		}
	}
	var shift int64
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		expNeg := false
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			expNeg = s[i] == '-'
			i++
		}
		e := run()
		if e == "" {
			return false, "", 0, false
		}
		// Past 9 digits the exponent only has to be known to be huge.
		shift = 1 << 40
		if e = strings.TrimLeft(e, "0"); len(e) <= 9 {
			shift = 0
			for _, d := range []byte(e) {
				shift = 10*shift + int64(d-'0')
			}
		}
		if expNeg {
			shift = -shift
		}
	}
	if i != len(s) {
		return false, "", 0, false
	}
	all := whole + frac
	trimmed := strings.TrimLeft(all, "0")
	exp = int64(len(whole)-(len(all)-len(trimmed))) + shift
	return neg, strings.TrimRight(trimmed, "0"), exp, true
}

// shorten is s as an error quotes it: whole when short, else its start and its length.
func shorten(s string) string {
	const limit = 40
	n := utf8.RuneCountInString(s)
	if n <= limit {
		return s
	}
	cut := 0
	for range limit - 8 {
		_, size := utf8.DecodeRuneInString(s[cut:])
		cut += size
	}
	return fmt.Sprintf("%s… (%d characters)", s[:cut], n)
}

// countValue reads a count: a whole number, at least 0.
func countValue(v any) (int, error) {
	s, err := scalarText(v, "int")
	if err != nil {
		return 0, errors.New("a count takes a whole number")
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, errors.New("a count takes a whole number, at least 0")
	}
	if n > 1<<16 {
		return 0, errors.New("the count is too large")
	}
	return n, nil
}

// stdinBytes spells the stdin parameter: a JSON or YAML document as JSON, a text format's
// string as it is.
func stdinBytes(st *Stdin, v any) ([]byte, error) {
	switch st.Format {
	case "json", "yaml":
		b, err := json.Marshal(v)
		if err != nil {
			return nil, &ArgvError{Param: st.Param, Reason: err.Error()}
		}
		return append(b, '\n'), nil
	}
	s, ok := v.(string)
	if !ok {
		return nil, &ArgvError{Param: st.Param, Reason: "stdin in this format takes a string"}
	}
	return []byte(s), nil
}

func compactJSON(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", fmt.Errorf("can't write the value as JSON: %w", err)
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// elemType is a list type's element type ("[]int" → "int").
func elemType(typ string) string {
	return strings.TrimPrefix(typ, "[]")
}

// mapValueType is a map type's value type ("map[string]int" → "int").
func mapValueType(typ string) string {
	if i := strings.Index(typ, "]"); strings.HasPrefix(typ, "map[") && i >= 0 {
		return typ[i+1:]
	}
	return ""
}

func isIntType(typ string) bool {
	switch typ {
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "integer", "count":
		return true
	}
	return false
}

func jsonKind(v any) string {
	switch v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	}
	return fmt.Sprintf("%T", v)
}
