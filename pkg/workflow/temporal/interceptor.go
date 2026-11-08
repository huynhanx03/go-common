package temporal

import (
	"context"
	"strconv"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/interceptor"
	sdklog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/workflow"

	"github.com/huynhanx03/go-common/pkg/logger"
)

type observabilityInterceptor struct {
	interceptor.WorkerInterceptorBase
}

func (*observabilityInterceptor) InterceptActivity(_ context.Context, next interceptor.ActivityInboundInterceptor) interceptor.ActivityInboundInterceptor {
	return &activityObservability{
		ActivityInboundInterceptorBase: interceptor.ActivityInboundInterceptorBase{Next: next},
	}
}

func (*observabilityInterceptor) InterceptWorkflow(_ workflow.Context, next interceptor.WorkflowInboundInterceptor) interceptor.WorkflowInboundInterceptor {
	return &workflowObservability{
		WorkflowInboundInterceptorBase: interceptor.WorkflowInboundInterceptorBase{Next: next},
	}
}

type activityObservability struct {
	interceptor.ActivityInboundInterceptorBase
}

func (i *activityObservability) ExecuteActivity(ctx context.Context, input *interceptor.ExecuteActivityInput) (any, error) {
	info := activity.GetInfo(ctx)
	ctx = logger.WithFields(ctx,
		logger.String(workflowIDField, info.WorkflowExecution.ID),
		logger.String(runIDField, info.WorkflowExecution.RunID),
		logger.String(activityIDField, info.ActivityID),
		logger.String(activityAttemptField, strconv.Itoa(int(info.Attempt))),
	)
	return i.Next.ExecuteActivity(ctx, input)
}

type workflowObservability struct {
	interceptor.WorkflowInboundInterceptorBase
}

func (i *workflowObservability) Init(outbound interceptor.WorkflowOutboundInterceptor) error {
	return i.WorkflowInboundInterceptorBase.Init(&workflowLogging{
		WorkflowOutboundInterceptorBase: interceptor.WorkflowOutboundInterceptorBase{Next: outbound},
	})
}

type workflowLogging struct {
	interceptor.WorkflowOutboundInterceptorBase
}

func (w *workflowLogging) GetLogger(ctx workflow.Context) sdklog.Logger {
	return sdklog.With(w.Next.GetLogger(ctx), workflowLogFields(ctx)...)
}

var _ interceptor.WorkerInterceptor = (*observabilityInterceptor)(nil)
