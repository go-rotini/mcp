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

var _ rotini.HandlerE = (*rotiniMcpConfigListHandler)(nil)

type rotiniMcpConfigListHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*rotiniMcpConfigListHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[RotiniMcpConfigListInputs]()
	if err != nil {
		return err
	}
	f, a := inputs.RotiniMcpConfigList.Flags, inputs.RotiniMcpConfigList.Arguments

	_, servers, err := clients.List(clients.Client(a.Client), clients.Scope(f.Scope), clients.Dirs{Project: rtx.Dir()})
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, name := range slices.Sorted(maps.Keys(servers)) {
		s := servers[name]
		fmt.Fprintf(&b, "%s\t%s\n", name, strings.Join(append([]string{s.Command}, s.Args...), " "))
	}
	_, err = fmt.Fprint(rtx.Stdout, b.String())
	return err
}
