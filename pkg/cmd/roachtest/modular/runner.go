package modular

import (
	"context"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

type runner struct {
	executor Executor
}

func NewRunner(executor Executor) *runner {
	return &runner{
		executor: executor,
	}
}

func (r *runner) Run(ctx context.Context, l *logger.Logger, plan *TestPlan) error {
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
	// TODO: implement helper creation. Investigate how the mixed version framework
	// handles this, as there may be some nuances around:
	// 1. Keeping track of modified state between steps
	// 2. Handling cluster connections
	// 3. Maintaining per step state.
	return nil
}
