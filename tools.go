package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/go-rotini/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// MetaKey is the `_meta` key a tool's invocation facts live under in a tools document.
const MetaKey = "dev.rotini/invoke"

// The value kinds an invocation fact names: how a parameter's value is written on the command
// line.
const (
	KindScalar = "scalar" // one attached value (--name=value), or a bool's bare flag
	KindCount  = "count"  // the bare flag, repeated
	KindList   = "list"   // one occurrence per item
	KindMap    = "map"    // one key=value occurrence per entry
	KindObject = "object" // JSON, one occurrence per element of a list of objects
)

// Tool is one tool of a tools document: its MCP definition and the facts that turn a call back
// into a command line.
type Tool struct {
	Name        string
	Title       string
	Description string
	Annotations *sdk.ToolAnnotations
	Invoke      Invoke

	raw    map[string]any // the definition as read
	input  map[string]any // inputSchema
	output map[string]any // outputSchema, or nil
}

// Invoke is how a tool call becomes a command line: rotini's `_meta["dev.rotini/invoke"]`.
type Invoke struct {
	Path           []string        `json:"path"`
	Arguments      []Argument      `json:"arguments,omitempty"`
	Flags          map[string]Flag `json:"flags,omitempty"`
	Fixed          []string        `json:"fixed,omitempty"`
	Stdin          *Stdin          `json:"stdin,omitempty"`
	Passthrough    bool            `json:"passthrough,omitempty"` // the command takes every word after its path as typed: no "--"
	Stream         bool            `json:"stream,omitempty"`
	Wrapped        bool            `json:"wrapped,omitempty"`
	ResponsePrefix string          `json:"response_prefix,omitempty"` // the program reads response files: a raw word before "--" starting with it has it doubled
}

// Argument is a positional parameter, in command-line order.
type Argument struct {
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Variadic    bool     `json:"variadic,omitempty"`
	Passthrough bool     `json:"passthrough,omitempty"` // it and every later word are taken as typed; "--" still comes first
	Secret      bool     `json:"secret,omitempty"`
	From        []string `json:"from,omitempty"`      // file: a leading @ reads a file; stdin: "-" reads stdin
	Separator   string   `json:"separator,omitempty"` // each item is quoted on its own as an item of a list split on it
}

// Flag is how one flag parameter is written.
type Flag struct {
	Flag       string   `json:"flag"`
	Kind       string   `json:"kind"`
	Type       string   `json:"type,omitempty"`
	Negated    string   `json:"negated,omitempty"`
	Separator  string   `json:"separator,omitempty"`
	Role       string   `json:"role,omitempty"`
	Secret     bool     `json:"secret,omitempty"`
	From       []string `json:"from,omitempty"`        // file: a leading @ reads a file; stdin: "-" reads stdin
	HasDefault bool     `json:"has_default,omitempty"` // a count with a default: leaving it out isn't 0
	DottedKeys bool     `json:"dotted_keys,omitempty"` // a map whose nested keys are written flattened: a.b=value
}

// Stdin is the parameter whose value the command reads on stdin, and its format (json, yaml,
// lines, jsonl or text).
type Stdin struct {
	Param  string `json:"param"`
	Format string `json:"format"`
}

// ParseTools reads a tools document: the `tools/mcp.json` file Rotini's tools feature writes
// (or the generated `ToolsMCP` string), an MCP tools/list result whose tools carry their
// invocation facts.
func ParseTools(doc []byte) ([]Tool, error) {
	var file struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(doc, &file); err != nil {
		return nil, fmt.Errorf("mcp: the tools document isn't JSON: %w", err)
	}
	if file.Tools == nil {
		return nil, errors.New(`mcp: the tools document has no "tools" list`)
	}
	tools := make([]Tool, 0, len(file.Tools))
	seen := map[string]bool{}
	for i, raw := range file.Tools {
		t, err := parseTool(raw)
		if err != nil {
			return nil, fmt.Errorf("mcp: tool %d: %w", i, err)
		}
		if seen[t.Name] {
			return nil, fmt.Errorf("mcp: two tools are named %q", t.Name)
		}
		seen[t.Name] = true
		tools = append(tools, t)
	}
	return tools, nil
}

func parseTool(raw json.RawMessage) (Tool, error) {
	var m map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		return Tool{}, fmt.Errorf("not a JSON object: %w", err)
	}
	var head struct {
		Name        string               `json:"name"`
		Title       string               `json:"title"`
		Description string               `json:"description"`
		Annotations *sdk.ToolAnnotations `json:"annotations"`
		Meta        map[string]Invoke    `json:"_meta"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return Tool{}, fmt.Errorf("can't read it: %w", err)
	}
	if head.Name == "" {
		return Tool{}, errors.New("it has no name")
	}
	inv, ok := head.Meta[MetaKey]
	if !ok {
		return Tool{}, fmt.Errorf("%q has no _meta[%q]; write the tools document with Rotini's tools feature", head.Name, MetaKey)
	}
	if err := inv.check(); err != nil {
		return Tool{}, fmt.Errorf("%q: %w", head.Name, err)
	}
	in, _ := m["inputSchema"].(map[string]any)
	if in == nil || in["type"] != "object" {
		return Tool{}, fmt.Errorf(`%q: its inputSchema must be an object schema ("type": "object")`, head.Name)
	}
	out, _ := m["outputSchema"].(map[string]any)
	return Tool{
		Name:        head.Name,
		Title:       head.Title,
		Description: head.Description,
		Annotations: head.Annotations,
		Invoke:      inv,
		raw:         m,
		input:       in,
		output:      out,
	}, nil
}

// check rejects facts a command line can't be built from.
func (inv Invoke) check() error {
	if inv.Path == nil {
		return errors.New("its invocation facts have no path")
	}
	for _, a := range inv.Arguments {
		if a.Name == "" || !slices.Contains([]string{KindScalar, KindList, KindMap, KindObject}, a.Kind) {
			return fmt.Errorf("the argument %q has the unknown kind %q", a.Name, a.Kind)
		}
	}
	for name, f := range inv.Flags {
		if f.Flag == "" {
			return fmt.Errorf("the flag parameter %q names no flag", name)
		}
		if !slices.Contains([]string{KindScalar, KindCount, KindList, KindMap, KindObject}, f.Kind) {
			return fmt.Errorf("the flag parameter %q has the unknown kind %q", name, f.Kind)
		}
	}
	if inv.Stdin != nil && inv.Stdin.Param == "" {
		return errors.New("its stdin fact names no parameter")
	}
	return nil
}

// InputSchema is the tool's input schema, as a JSON value.
func (t Tool) InputSchema() map[string]any { return jsonCopy(t.input) }

// OutputSchema is the tool's output schema as a JSON value, or nil when it declares none.
func (t Tool) OutputSchema() map[string]any {
	if t.output == nil {
		return nil
	}
	return jsonCopy(t.output)
}

// Parameters lists the tool's parameter names, sorted.
func (t Tool) Parameters() []string {
	props, _ := t.input["properties"].(map[string]any)
	return slices.Sorted(maps.Keys(props))
}

// definition is the tool as tools/list serves it: the document's definition, with the schemas
// as adjusted by options.
func (t Tool) definition() (*sdk.Tool, error) {
	m := maps.Clone(t.raw)
	m["inputSchema"] = t.input
	if t.output != nil {
		m["outputSchema"] = t.output
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("mcp: tool %q: %w", t.Name, err)
	}
	var def sdk.Tool
	if err := json.Unmarshal(b, &def); err != nil {
		return nil, fmt.Errorf("mcp: tool %q: %w", t.Name, err)
	}
	return &def, nil
}

// compile compiles a tool schema as JSON Schema 2020-12, the dialect MCP tools use.
func compile(name, which string, schema map[string]any) (*jsonschema.Schema, error) {
	b, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("mcp: tool %q: the %s schema: %w", name, which, err)
	}
	s, err := jsonschema.Compile(b, jsonschema.WithDefaultDraft(jsonschema.Draft202012))
	if err != nil {
		return nil, fmt.Errorf("mcp: tool %q: the %s schema doesn't compile: %w", name, which, err)
	}
	return s, nil
}

// jsonCopy deep-copies a JSON value.
func jsonCopy[T any](v T) T {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out T
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if dec.Decode(&out) != nil {
		return v
	}
	return out
}
