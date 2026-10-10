package rotinimcp

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*rotiniMcpHandler)(nil)

type rotiniMcpHandler struct {
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*rotiniMcpHandler) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[RotiniMcpInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}

	if inputs.RotiniMcp.Flags.Help {
		var path []string
		for _, c := range rtx.CommandChain()[1:] {
			path = append(path, c.Name)
		}

		page, err := Help(path...)
		if err != nil {
			rtx.HaltWith(err)
			return
		}

		if _, err := fmt.Fprintln(rtx.Stdout, page); err != nil {
			rtx.HaltWith(err)
			return
		}

		rtx.HaltWithCode(0)
		return
	}

	if inputs.RotiniMcp.Flags.Version {
		version := rtx.Version()
		if version == "" {
			version = "unknown"
		}

		if _, err := fmt.Fprintln(rtx.Stdout, rtx.CommandChain()[0].Name, version); err != nil {
			rtx.HaltWith(err)
			return
		}

		rtx.HaltWithCode(0)
		return
	}
}

func (*rotiniMcpHandler) Run(ctx context.Context, rtx *rotini.Context) {
	fmt.Fprintln(rtx.Stderr, rtx.Help())
	rtx.HaltWithCode(1)
}
