package delegation

import (
	"context"
	"fmt"
	"regexp"

	"github.com/clnkr-ai/clnkr"
)

type ReadOnlyExecutor struct {
	inner clnkr.Executor
}

func NewReadOnlyExecutor(inner clnkr.Executor) *ReadOnlyExecutor {
	return &ReadOnlyExecutor{inner: inner}
}

func (e *ReadOnlyExecutor) Execute(ctx context.Context, command string, dir string) (clnkr.CommandResult, error) {
	if reason := denyWriteCommandReason(command); reason != "" {
		return clnkr.CommandResult{
			Command: command,
			Outcome: clnkr.CommandOutcome{
				Type:    clnkr.CommandOutcomeDenied,
				Message: reason,
			},
		}, fmt.Errorf("read-only child probe denied command: %s", reason)
	}
	return e.inner.Execute(ctx, command, dir)
}

func (e *ReadOnlyExecutor) SetEnv(env map[string]string) {
	if setter, ok := e.inner.(clnkr.ExecutorStateSetter); ok {
		setter.SetEnv(env)
	}
}

var readOnlyDeniedPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(^|[;&|]\s*)(touch|rm|mv|cp|mkdir|rmdir|chmod|chown|truncate)\b`),
	regexp.MustCompile(`(^|[;&|]\s*)tee\b`),
	regexp.MustCompile(`\bsed\s+(-[^ ]*i|--in-place)\b`),
	regexp.MustCompile(`(^|[^<])>>?[^&]`),
}

func denyWriteCommandReason(command string) string {
	for _, pattern := range readOnlyDeniedPatterns {
		if pattern.MatchString(command) {
			return "command may modify the workspace"
		}
	}
	return ""
}
