package mcp_test

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-rotini/mcp"
)

func ExampleInvoke_Argv() {
	doc, err := os.ReadFile(filepath.Join("testdata", "taskr.mcp.json"))
	if err != nil {
		panic(err)
	}
	tools, err := mcp.ParseTools(doc)
	if err != nil {
		panic(err)
	}
	for _, t := range tools {
		if t.Name != "taskr_add" {
			continue
		}
		argv, _, err := t.Invoke.Argv(map[string]any{"title": "-write docs", "tag": []any{"work"}, "verbose": 2})
		if err != nil {
			panic(err)
		}
		fmt.Println(argv)
	}
	// Output: [add --tag=work --verbose --verbose -- -write docs]
}

func ExampleParseTools() {
	doc := []byte(`{"tools": [{
		"name": "taskr_list",
		"inputSchema": {"type": "object", "properties": {"page": {"type": "string"}}},
		"_meta": {"dev.rotini/invoke": {"path": ["list"], "fixed": ["--format=json"],
			"flags": {"page": {"flag": "--page", "kind": "scalar", "type": "string"}}}}
	}]}`)
	tools, err := mcp.ParseTools(doc)
	if err != nil {
		panic(err)
	}
	t := tools[0]
	fmt.Println(t.Name, t.Invoke.Path, t.Parameters(), t.OutputSchema() == nil)
	// Output: taskr_list [list] [page] true
}
