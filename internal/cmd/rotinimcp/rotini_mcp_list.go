package rotinimcp

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/go-rotini/rotini"

	"github.com/go-rotini/mcp/clients"
)

var _ rotini.HandlerE = (*rotiniMcpListHandler)(nil)

type rotiniMcpListHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*rotiniMcpListHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[RotiniMcpListInputs]()
	if err != nil {
		return err
	}
	f, a := inputs.RotiniMcpList.Flags, inputs.RotiniMcpList.Arguments

	which := clients.Clients()
	if a.Client != "" {
		which = []clients.Client{clients.Client(a.Client)}
	}
	w := tabwriter.NewWriter(rtx.Stdout, 0, 4, 2, ' ', 0)
	for _, c := range which {
		for _, scope := range c.Scopes() {
			if f.Scope != "" && scope != clients.Scope(f.Scope) {
				continue
			}
			_, servers, err := clients.List(c, scope, clients.Dirs{Project: rtx.Dir()})
			if err != nil {
				if errors.Is(err, clients.ErrScope) {
					continue
				}
				return err
			}
			for _, name := range slices.Sorted(maps.Keys(servers)) {
				s := servers[name]
				line := strings.Join([]string{string(c), string(scope), name, strings.Join(append([]string{s.Command}, s.Args...), " ")}, "\t")
				if _, err := w.Write([]byte(line + "\n")); err != nil {
					return err
				}
			}
		}
	}
	return w.Flush()
}
