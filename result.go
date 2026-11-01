package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"iter"
	"strconv"
	"strings"

	"github.com/go-rotini/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The `_meta` keys of a call result.
const (
	metaExitCode = "dev.rotini/exit_code"
	metaStderr   = "dev.rotini/stderr"
	metaTrailing = "dev.rotini/trailing_stdout"
	metaError    = "dev.rotini/error"
	metaRecords  = "dev.rotini/records"
)

// errorSchemaVersion is the StructuredReporter line version this module reads.
const errorSchemaVersion = 1

// result turns a finished run into the call's result.
//
// A zero exit is a success. With an output schema, stdout is the structured content (one JSON
// value, or one per line for a stream), checked against the schema, with its JSON as the text;
// text after the value (the default reporter's messages) goes in _meta. Without one, stdout is
// the text. On success, stderr (with that trailing text) is shown as described at
// [SuccessStderrContent], unless mode is [SuccessStderrMeta]. A non-zero exit is an error: the text is stderr, and a StructuredReporter error line
// on stderr is the structured content, or with an output schema (which clients check structured
// content against, even on an error) goes in _meta. Every StructuredReporter line on stderr goes
// in _meta too.
func (t *tool) result(r *run, mode SuccessStderr) *sdk.CallToolResult {
	meta := sdk.Meta{metaExitCode: r.code}
	if records := reportLines(r.stderr); len(records) > 0 {
		meta[metaRecords] = records
	}
	switch {
	case r.failure != "":
		return errorResult(meta, r.failure)
	case r.overLimit:
		return errorResult(meta, stoppedText("the command's output passed the server's output limit; it was stopped", r))
	case r.timedOut:
		if !r.serverTimeout {
			return errorResult(meta, stoppedText("the command ran past the call's deadline; it was stopped", r))
		}
		return errorResult(meta, stoppedText("the command ran past the server's timeout; it was stopped", r))
	case r.code != 0:
		return t.failure(meta, r)
	}
	stderr := strings.TrimSpace(string(r.stderr))
	if stderr != "" {
		meta[metaStderr] = stderr
	}
	if mode != SuccessStderrContent {
		stderr = ""
	}
	if t.out == nil {
		if len(r.stdout) == 0 && stderr != "" {
			return &sdk.CallToolResult{Meta: meta, Content: []sdk.Content{&sdk.TextContent{Text: stderr}}}
		}
		return &sdk.CallToolResult{Meta: meta, Content: withMessages([]sdk.Content{&sdk.TextContent{Text: string(r.stdout)}}, stderr)}
	}
	v, trailing, err := decodeOutput(r.stdout, t.Invoke.Stream)
	if err != nil {
		return errorResult(meta, fmt.Sprintf("the command declares JSON output, but stdout isn't JSON: %v", err))
	}
	if trailing != "" {
		meta[metaTrailing] = trailing
	}
	if t.Invoke.Wrapped {
		v = map[string]any{"result": v}
	}
	text, err := compactJSON(v)
	if err != nil {
		return errorResult(meta, err.Error())
	}
	if msg := validationMessage(t.out, []byte(text)); msg != "" {
		return errorResult(meta, "the command's output doesn't match the tool's output schema: "+msg)
	}
	if mode == SuccessStderrContent {
		stderr = strings.TrimSpace(trailing + "\n" + stderr)
	}
	return &sdk.CallToolResult{Meta: meta, StructuredContent: v, Content: withMessages([]sdk.Content{&sdk.TextContent{Text: text}}, stderr)}
}

// withMessages adds a run's messages to its content as a text block of their own, when it has
// any.
func withMessages(content []sdk.Content, messages string) []sdk.Content {
	if messages == "" {
		return content
	}
	return append(content, &sdk.TextContent{Text: messages})
}

// failure is the result of a non-zero exit.
func (t *tool) failure(meta sdk.Meta, r *run) *sdk.CallToolResult {
	text := strings.TrimSpace(string(r.stderr))
	if text == "" {
		text = strings.TrimSpace(string(r.stdout))
	}
	if text == "" {
		text = fmt.Sprintf("the command exited with code %d", r.code)
	}
	res := errorResult(meta, text)
	switch line := errorLine(r.stderr); {
	case line == nil:
	case t.out == nil:
		res.StructuredContent = line
	default:
		meta[metaError] = line
	}
	return res
}

// stoppedTail is how much of a stopped run's output its result keeps.
const stoppedTail = 4 << 10

// stoppedText is the text of a run that was stopped: why, then the end of what it wrote on
// stderr (or on stdout, when stderr is empty), which often says why it hung.
func stoppedText(why string, r *run) string {
	if t := tail(r.stderr, stoppedTail); t != "" {
		return why + "\n\nthe end of its stderr:\n" + t
	}
	if t := tail(r.stdout, stoppedTail); t != "" {
		return why + "\n\nthe end of its stdout:\n" + t
	}
	return why
}

func errorResult(meta sdk.Meta, text string) *sdk.CallToolResult {
	return &sdk.CallToolResult{Meta: meta, IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: text}}}
}

// decodeOutput reads the command's JSON output: one value, or for a stream every value up to
// the first text that isn't one (an empty stream is an empty array). trailing is the text after
// them.
func decodeOutput(stdout []byte, stream bool) (v any, trailing string, err error) {
	dec := json.NewDecoder(bytes.NewReader(stdout))
	dec.UseNumber()
	if !stream {
		if err := dec.Decode(&v); err != nil {
			return nil, "", err
		}
		return v, strings.TrimSpace(string(stdout[dec.InputOffset():])), nil
	}
	items := []any{}
	offset := int64(0)
	for {
		var item any
		if dec.Decode(&item) != nil {
			break
		}
		items = append(items, item)
		offset = dec.InputOffset()
	}
	return items, strings.TrimSpace(string(stdout[offset:])), nil //nolint:nilerr // the first text that isn't JSON ends the stream
}

// errorLine finds a StructuredReporter error line on stderr: a JSON object with an "error"
// member and either a schema_version this module reads or no schema_version.
func errorLine(stderr []byte) map[string]any {
	for m := range jsonLines(stderr) {
		if m["error"] == nil {
			continue
		}
		if v, ok := m["schema_version"]; ok && v != json.Number(strconv.Itoa(errorSchemaVersion)) {
			continue
		}
		return m
	}
	return nil
}

// reportLines lists the StructuredReporter lines on stderr, in order: each a JSON object
// holding a schema_version this module reads and one record (an error, warning, info or
// success, or a record of a channel the program's spec declares, under its channel's name).
func reportLines(stderr []byte) []any {
	var lines []any
	for m := range jsonLines(stderr) {
		if len(m) != 2 || m["schema_version"] != json.Number(strconv.Itoa(errorSchemaVersion)) {
			continue
		}
		for _, v := range m {
			if _, ok := v.(map[string]any); ok {
				lines = append(lines, m)
			}
		}
	}
	return lines
}

// jsonLines yields each line of out that is a JSON object, numbers as json.Number.
func jsonLines(out []byte) iter.Seq[map[string]any] {
	return func(yield func(map[string]any) bool) {
		for line := range strings.Lines(string(out)) {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "{") {
				continue
			}
			var m map[string]any
			dec := json.NewDecoder(strings.NewReader(line))
			dec.UseNumber()
			if dec.Decode(&m) != nil {
				continue
			}
			if !yield(m) {
				return
			}
		}
	}
}

// validationMessage checks a JSON document against a schema: "" when it matches, else the
// failures, one per line.
func validationMessage(s *jsonschema.Schema, doc []byte) string {
	res, err := s.Validate(doc, jsonschema.WithFormatAssertion(true))
	if err != nil {
		return err.Error()
	}
	if res.Valid {
		return ""
	}
	var msgs []string
	var walk func([]jsonschema.ValidationError)
	walk = func(errs []jsonschema.ValidationError) {
		for _, e := range errs {
			if len(e.Causes) > 0 {
				walk(e.Causes)
				continue
			}
			loc := e.InstanceLocation
			if loc == "" {
				loc = "/"
			}
			msgs = append(msgs, loc+": "+e.Message)
		}
	}
	walk(res.Errors)
	return strings.Join(msgs, "\n")
}
