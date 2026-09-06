package tools

import (
	"context"
	"fmt"
	"io"
)

// BuildRequest is one invocation of one external tool.
//
// TODO(andrea): a real map build is a *sequence* of tools (compile, vis, light,
// package), not one — so this will grow into a pipeline of stages once the tool
// set is chosen. It is deliberately one stage today: a multi-stage pipeline
// designed around tools nobody has picked would encode guesses about which
// stages exist and what they hand each other.
type BuildRequest struct {
	// Tool is the tool name; empty means the fake tool.
	Tool string
	// Version is the tool version; empty means whatever the manager resolves
	// by default.
	Version string
	// Args is the tool's argv tail.
	Args []string
}

// Build runs one tool end to end: resolve the reference, make sure it is
// downloaded and checksum-verified, then run it as a separate process with its
// output streamed to the given writers.
//
// This is the whole pipeline in one place so the CLI and the GUI drive the same
// sequence — `companion build` streams into the terminal, POST /api/build
// buffers into a response, and neither one re-implements the steps.
func Build(ctx context.Context, manager Manager, request BuildRequest, stdout, stderr io.Writer) error {
	if manager == nil {
		return fmt.Errorf("tools: no tool manager")
	}
	name := request.Tool
	if name == "" {
		name = NoopToolName
	}

	ref, err := manager.Resolve(name, request.Version)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "resolved %s\n", ref)

	path, err := manager.EnsureDownloaded(ctx, ref)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "verified %s\n", path)

	return manager.Run(ctx, path, request.Args, stdout, stderr)
}
