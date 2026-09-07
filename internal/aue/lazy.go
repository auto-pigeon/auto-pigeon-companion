package aue

import (
	"context"
	"sync"
)

// LazyRunner resolves an extractor the first time one is actually needed.
//
// # Why the server does not resolve at startup
//
// Resolving reaches the network: it verifies a catalogue and a compatibility
// manifest. A local server that did that before it would listen would be a
// server that starts slowly on a good connection and not at all on a bad one,
// for a feature the user may never touch in that session. So the server holds
// one of these, answers "is there an extractor" from [Resolver.Status] — which
// reads the local cache and the recorded pin and touches nothing — and resolves
// on the first real invocation.
//
// The resolution is memoised, including its failure. A server whose extractor
// could not be resolved must not try again on every request: the failure is a
// property of this machine's configuration, not of the request, and retrying it
// per request turns one clear error into a repeated network call.
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
// what the local status says. It never triggers a resolution: a caller asking
// where an extractor came from must not cause one to be fetched.
func (l *LazyRunner) Provenance() Provenance {
	if l.runner != nil {
		return l.runner.Provenance()
	}
	status := l.Resolver.Status()

	return Provenance{Mode: status.Mode, Verified: status.Verified, Version: status.Version, Note: status.Note}
}

// Status is the local, network-free answer.
func (l *LazyRunner) Status() Status { return l.Resolver.Status() }

// Available reports whether this Companion has an extractor it could run,
// without resolving one.
func (l *LazyRunner) Available() bool { return l.Resolver.Status().Available }
