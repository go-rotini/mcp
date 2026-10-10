package mcp

import (
	"io"
	"log/slog"
	"strings"
)

// wrapOutput adapts a tool's output to MCP 2025-11-25, where structured content is an object:
// a non-object output becomes {"result": …}, and an object reached through a root $ref gets
// "type": "object" beside it. A document written for that revision is left as it is.
func wrapOutput(t *Tool) {
	o := t.output
	if isObjectSchema(o) {
		o["type"] = "object"
		return
	}
	wrapped := map[string]any{
		"type":       "object",
		"properties": map[string]any{"result": o},
		"required":   []any{"result"},
	}
	if defs, ok := o["$defs"]; ok {
		wrapped["$defs"] = defs // "#/$defs/…" references resolve from the root
		delete(o, "$defs")
	}
	t.output = wrapped
	t.Invoke.Wrapped = true
}

// isObjectSchema reports whether a schema describes a JSON object, following a root $ref into
// its $defs.
func isObjectSchema(s map[string]any) bool {
	defs, _ := s["$defs"].(map[string]any)
	cur := s
	for range 8 {
		ref, ok := cur["$ref"].(string)
		if !ok {
			break
		}
		name, ok := strings.CutPrefix(ref, "#/$defs/")
		d, _ := defs[name].(map[string]any)
		if !ok || d == nil {
			return false
		}
		cur = d
	}
	return cur["type"] == "object"
}

func newLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, nil))
}
