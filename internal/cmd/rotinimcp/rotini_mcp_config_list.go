package rotinimcp

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/go-rotini/rotini"

	"github.com/go-rotini/mcp/clients"
)

var _ rotini.Handler = (*rotiniMcpConfigListHandler)(nil)

type rotiniMcpConfigListHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*rotiniMcpConfigListHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[RotiniMcpConfigListInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	f, a := inputs.RotiniMcpConfigList.Flags, inputs.RotiniMcpConfigList.Arguments

	_, servers, err := clients.List(clients.Client(a.Client), clients.Scope(f.Scope), clients.Dirs{Project: rtx.Dir()})
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	var b strings.Builder
	for _, name := range slices.Sorted(maps.Keys(servers)) {
		s := servers[name]
		fmt.Fprintf(&b, "%s\t%s\n", name, strings.Join(append([]string{s.Command}, s.Args...), " "))
	}
	if _, err := fmt.Fprint(rtx.Stdout, b.String()); err != nil {
		rtx.HaltWith(err)
	}
}
