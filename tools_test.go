package mcp

import (
	"encoding/json"
	"testing"
)

func TestParseTools_taskr(t *testing.T) {
	tools := taskrTools(t)
	add := tools["taskr_add"]
	if add.Title != "taskr add" || add.Annotations == nil || add.Annotations.ReadOnlyHint {
		t.Errorf("taskr_add = %+v", add)
	}
	if got := add.Parameters(); len(got) != 5 || got[0] != "due" {
		t.Errorf("parameters = %v", got)
	}
	if add.OutputSchema()["$ref"] != "#/$defs/Task" {
		t.Errorf("output schema = %v", add.OutputSchema())
	}
	in := add.InputSchema()
	in["type"] = "changed"
	if add.InputSchema()["type"] != "object" {
		t.Error("InputSchema returned the tool's own map")
	}
	if tools["taskr_purge"].OutputSchema() != nil {
		t.Error("purge has an output schema")
	}
	if tools["taskr_list"].Invoke.Fixed[0] != "--format=json" {
		t.Error("list lost its machine-output flag")
	}
}

func TestWrapOutput(t *testing.T) {
	cases := []struct {
		name, schema, want string
		wrapped            bool
	}{
		{"object", `{"type":"object"}`, `{"type":"object"}`, false},
		{"ref to object", `{"$defs":{"T":{"type":"object"}},"$ref":"#/$defs/T"}`, `{"$defs":{"T":{"type":"object"}},"$ref":"#/$defs/T","type":"object"}`, false},
		{"array", `{"type":"array","items":{"$ref":"#/$defs/T"},"$defs":{"T":{"type":"object"}}}`,
			`{"$defs":{"T":{"type":"object"}},"properties":{"result":{"items":{"$ref":"#/$defs/T"},"type":"array"}},"required":["result"],"type":"object"}`, true},
		{"ref to string", `{"$defs":{"S":{"type":"string"}},"$ref":"#/$defs/S"}`,
			`{"$defs":{"S":{"type":"string"}},"properties":{"result":{"$ref":"#/$defs/S"}},"required":["result"],"type":"object"}`, true},
		{"dangling ref", `{"$ref":"#/$defs/Missing"}`, `{"properties":{"result":{"$ref":"#/$defs/Missing"}},"required":["result"],"type":"object"}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out map[string]any
			if err := json.Unmarshal([]byte(tc.schema), &out); err != nil {
				t.Fatal(err)
			}
			tl := Tool{output: out}
			wrapOutput(&tl)
			b, _ := json.Marshal(tl.output)
			if string(b) != tc.want || tl.Invoke.Wrapped != tc.wrapped {
				t.Errorf("got %s (wrapped %v)\nwant %s (wrapped %v)", b, tl.Invoke.Wrapped, tc.want, tc.wrapped)
			}
		})
	}
}

func TestErrorLine(t *testing.T) {
	cases := map[string]bool{
		`{"schema_version":1,"error":{"message":"x"}}`:   true,
		`{"error":{"message":"x"}}`:                      true,
		`{"schema_version":1,"info":{"message":"x"}}`:    false,
		`{"schema_version":2,"error":{"message":"x"}}`:   false,
		`{"schema_version":1,"warning":{"message":"x"}}`: false,
		`Error: x`:  false,
		`{not json`: false,
	}
	for line, want := range cases {
		if got := errorLine([]byte("Error: before\n"+line+"\n")) != nil; got != want {
			t.Errorf("%s: found = %v, want %v", line, got, want)
		}
	}
}
