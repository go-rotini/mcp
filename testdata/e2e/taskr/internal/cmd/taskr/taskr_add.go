package taskr

import (
	"context"
	"encoding/json"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*taskrAddHandler)(nil)

type taskrAddHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*taskrAddHandler) Run(ctx context.Context, rtx *rotini.Context) {
	in, err := rtx.Inputs[TaskrAddInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	f := in.TaskrAdd.Flags
	out := map[string]any{
		"title":    in.TaskrAdd.Arguments.Title,
		"tags":     append([]string{}, f.Tag...),
		"priority": f.Priority,
		"done":     f.Done,
		"verbose":  in.Taskr.Flags.Verbose,
		"label":    map[string]string{},
		"note":     f.Note,
		"token":    f.Token != "",
	}
	for k, v := range f.Label {
		out["label"].(map[string]string)[k] = v
	}
	if err := json.NewEncoder(rtx.Stdout).Encode(out); err != nil {
		rtx.HaltWith(err)
	}
}
