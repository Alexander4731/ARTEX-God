package intercept

import (
	"context"
	"strconv"
)

// AllowAllTaskTools reads the current task's policy for each call, so changing
// it takes effect for running agents without changing the global rule cache.
func (i *Interceptor) AllowAllTaskTools(ctx context.Context) (bool, error) {
	taskID, _ := taskInfoFromCtx(ctx)
	id, err := strconv.ParseInt(taskID, 10, 64)
	if err != nil || id <= 0 {
		return false, nil
	}
	policy, err := i.db.GetTaskToolPolicy(ctx, id)
	return policy.AllowAll, err
}
