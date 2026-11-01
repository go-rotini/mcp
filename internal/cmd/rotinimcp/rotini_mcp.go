package rotinimcp

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.HandlerE = (*rotiniMcpHandler)(nil)

type rotiniMcpHandler struct {
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*rotiniMcpHandler) CascadingPreRun(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[RotiniMcpInputs]()
	if err != nil {
		return err
	}

	if inputs.RotiniMcp.Flags.Help {
		page, err := rtx.InvokedHelp()
		if err != nil {
			return err
		}

		if _, err := fmt.Fprintln(rtx.Stdout, page); err != nil {
			return err
		}

		rtx.HaltWithCode(0)
		return nil
	}

	if inputs.RotiniMcp.Flags.Version {
		version := rtx.Version()
		if version == "" {
			version = "unknown"
		}

		if _, err := fmt.Fprintln(rtx.Stdout, rtx.CommandChain()[0].Name, version); err != nil {
			return err
		}

		rtx.HaltWithCode(0)
		return nil
	}

	return nil
}

func (*rotiniMcpHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	fmt.Fprintln(rtx.Stderr, rtx.Help())
	rtx.HaltWithCode(1)
	return nil
}
