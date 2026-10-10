package taskr

import (
	"context"

	"github.com/go-rotini/mcp"
	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*taskrMcpHandler)(nil)

type taskrMcpHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*taskrMcpHandler) Run(ctx context.Context, rtx *rotini.Context) {
	err := mcp.Serve(ctx, []byte(ToolsMCP),
		mcp.WithStdio(rtx.Stdin, rtx.Stdout),
		mcp.WithEnv(rtx.Environ()),
		mcp.WithServingCommand("mcp"))
	if err != nil {
		rtx.HaltWith(err)
	}
}
