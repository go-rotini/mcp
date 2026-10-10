package taskr

import (
	"context"
	"errors"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*taskrFailHandler)(nil)

type taskrFailHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*taskrFailHandler) Run(ctx context.Context, rtx *rotini.Context) {
	rtx.HaltWith(errors.New("the list is locked"))
}
