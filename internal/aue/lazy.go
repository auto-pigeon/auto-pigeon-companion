package aue

import (
	"context"
	"sync"
)

// LazyRunner resolves an extractor the first time one is actually needed.
//
// # Why the server does not resolve at startup
//
// Resolving hashes the bundled executable and runs it once for the protocol
// handshake. A server that did that before it would listen would pay it for a
// feature the user may never touch in that session. So the server holds one of
// these, answers "is there an extractor" from [Resolver.Status] — which runs
// nothing — and resolves on the first real invocation.
//
// The resolution is memoised, including its failure. A server whose extractor
// could not be resolved must not try again on every request: the failure is a
// property of this installation, not of the request.
type LazyRunner struct {
	Resolver *Resolver

	once   sync.Once
	runner *ProcessRunner
	err    error
}

// NewLazyRunner wraps a resolver.
func NewLazyRunner(resolver *Resolver) *LazyRunner { return &LazyRunner{Resolver: resolver} }

func (l *LazyRunner) resolve(ctx context.Context) (*ProcessRunner, error) {
	l.once.Do(func() { l.runner, l.err = l.Resolver.Resolve(ctx) })

	return l.runner, l.err
}

// Run resolves on first use and then invokes.
func (l *LazyRunner) Run(ctx context.Context, subcommand string, args ...string) ([]byte, error) {
	runner, err := l.resolve(ctx)
	if err != nil {
		return nil, err
	}

	return runner.Run(ctx, subcommand, args...)
}

// Provenance is the resolved runner's, or — before anything has been resolved —
// what the local status says. It never triggers a resolution.
func (l *LazyRunner) Provenance() Provenance {
	if l.runner != nil {
		return l.runner.Provenance()
	}
	status := l.Resolver.Status()

	return Provenance{Mode: status.Mode, Verified: status.Verified, Path: status.Path, Note: status.Note}
}

// Status is the answer that runs nothing.
func (l *LazyRunner) Status() Status { return l.Resolver.Status() }

// Available reports whether this Companion has an extractor it could run,
// without resolving one.
func (l *LazyRunner) Available() bool { return l.Resolver.Status().Available }
