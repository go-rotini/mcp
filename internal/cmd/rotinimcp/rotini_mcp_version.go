package rotinimcp

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*rotiniMcpVersionHandler)(nil)

type rotiniMcpVersionHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*rotiniMcpVersionHandler) Run(ctx context.Context, rtx *rotini.Context) {
	version := rtx.Version()
	if version == "" {
		version = "unknown"
	}

	if _, err := fmt.Fprintln(rtx.Stdout, rtx.CommandChain()[0].Name, version); err != nil {
		rtx.HaltWith(err)
	}
}
