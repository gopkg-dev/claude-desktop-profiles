package profiles

import "context"

// Step names a stage of an operation that restarts Claude Desktop, reported as it begins.
type Step string

// The stages of a switch, an added account or a session sync, in order.
const (
	StepQuit   Step = "quit"   // Asking Claude Desktop to quit and waiting for it to save.
	StepSync   Step = "sync"   // Merging the other accounts' Code sessions.
	StepMove   Step = "move"   // Putting the account's data in place.
	StepLaunch Step = "launch" // Opening Claude Desktop again.
)

type progressKey struct{}

// WithProgress returns a context whose operations report each step to fn as it begins, on their own goroutine.
func WithProgress(ctx context.Context, fn func(Step)) context.Context {
	return context.WithValue(ctx, progressKey{}, fn)
}

// ReportStep tells ctx's progress function, if any, that step begins.
func ReportStep(ctx context.Context, step Step) {
	if fn, ok := ctx.Value(progressKey{}).(func(Step)); ok && fn != nil {
		fn(step)
	}
}
