package modular

import (
	"context"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

type runner struct {
	executor Executor
}

func (r *runner) Run(ctx context.Context, l *logger.Logger, plan TestPlan) error {
	for _, sp := range plan.stagePlans {
		for _, s := range sp.steps {
			h := r.newHelper()
			if err := s.Run(ctx, r.executor, l, h); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *runner) newHelper() *Helper {
	return &Helper{}
}
