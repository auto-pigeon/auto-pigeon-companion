package job

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// The per-job workspace, and what containment does and does not mean here.
//
// Every job gets its own directory. Inputs are *copied* into it, outputs are
// collected out of it, and it is removed when the job is cleaned up. The user's
// own files are read once, at staging time, and never written to, which is why
// cleaning up a job cannot lose somebody's map source: the job never had it,
// only a copy.
//
// # What is enforced
//
// Every path this package computes — a staged input's destination, a declared
// output's location, an artifact's published name — is resolved through any
// symlinks that already exist and then checked to be inside the directory it
// was declared under. The check is repeated *after* the process has run,
// because a tool can replace a file with a symlink between resolution and
// collection, and the second check is the one that catches it.
//
// # What is not enforced, said plainly
//
// This is not a sandbox. The Companion has no way, with the standard library
// and no privileges, to stop a program the user has authorised from writing
// wherever that user can write. What the containment checks stop is a
// *document* directing a program outside its declared roots, and the Companion
// publishing or reading something outside them. A tool that goes wandering on
// its own account is a tool the user chose to run, and pretending otherwise
// would be the more dangerous of the two claims.

// ErrEscapesRoot reports a path that resolved outside the directory it was
// declared under.
var ErrEscapesRoot = errors.New("job: path leaves the directory it was declared under")

// layout is one job's directory tree.
type layout struct {
	// Dir is the job's own directory: the record, the logs, everything.
	Dir string
	// Workspace is the `workspace` root the profile resolves against.
	Workspace string
	// Input is where staged copies of the user's files land, inside Workspace
	// so that a document can refer to them and the containment rules apply.
	Input string
	// Home and Temp are what the process gets instead of the user's own.
	Home string
	Temp string
	// Artifacts is where collected outputs are published.
	Artifacts string
}

func layoutFor(root, id string) layout {
	dir := filepath.Join(root, id)
	workspace := filepath.Join(dir, "workspace")
	return layout{
		Dir:       dir,
		Workspace: workspace,
		Input:     filepath.Join(workspace, "input"),
		Home:      filepath.Join(dir, "home"),
		Temp:      filepath.Join(dir, "tmp"),
		Artifacts: filepath.Join(dir, "artifacts"),
	}
}

// create builds the directories a job needs before anything is staged.
func (l layout) create() error {
	for _, dir := range []string{l.Dir, l.Workspace, l.Input, l.Home, l.Temp} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("job: creating %s: %w", dir, err)
		}
	}
	return nil
}

// remove deletes the workspace and its scratch directories, keeping the record,
// the logs and the published artifacts.
//
// The order matters: artifacts have already been moved out of the workspace by
// the time this runs, so nothing here can delete a result. And nothing here
// touches a path outside the job's own directory, which is what makes cleanup
// safe to run without asking.
func (l layout) remove() error {
	var problems []string
	for _, dir := range []string{l.Workspace, l.Home, l.Temp} {
		if err := os.RemoveAll(dir); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("job: cleaning up: %s", strings.Join(problems, "; "))
	}
	return nil
}

// resolveExisting resolves a path through the symlinks that exist today.
//
// A path whose final elements do not exist yet — an output a tool has not
// written — cannot be given to EvalSymlinks, which fails on a missing file. So
// the deepest existing ancestor is resolved and the rest is rejoined. That is
// the honest answer to "where would this end up": the part that exists is
// followed, and the part that does not cannot be pointing anywhere yet.
func resolveExisting(path string) (string, error) {
	path = filepath.Clean(path)
	remainder := ""
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			if remainder == "" {
				return resolved, nil
			}
			return filepath.Join(resolved, remainder), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("job: resolving %s: %w", path, err)
		}
		parent := filepath.Dir(path)
		if parent == path {
			// Reached the volume root without finding anything that exists.
			return filepath.Join(path, remainder), nil
		}
		remainder = filepath.Join(filepath.Base(path), remainder)
		path = parent
	}
}

// within reports whether candidate is inside root, both resolved through
// existing symlinks first.
func within(root, candidate string) error {
	resolvedRoot, err := resolveExisting(root)
	if err != nil {
		return err
	}
	resolvedCandidate, err := resolveExisting(candidate)
	if err != nil {
		return err
	}
	if pathWithin(resolvedRoot, resolvedCandidate) {
		return nil
	}
	return fmt.Errorf("%w: %s is not inside %s", ErrEscapesRoot, candidate, root)
}

// pathWithin is the comparison, split out so the case rule has one home.
func pathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return true
	}
	if runtime.GOOS != "windows" {
		return false
	}
	// Windows paths are case-insensitive, so `C:\Tools` and `c:\tools` name one
	// directory. Refusing the second spelling would be a false alarm, not a
	// protection.
	rel, err = filepath.Rel(strings.ToLower(root), strings.ToLower(candidate))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// withinAny reports whether candidate is inside at least one of the roots.
func withinAny(roots []string, candidate string) error {
	if len(roots) == 0 {
		return fmt.Errorf("%w: %s, and no root was declared for it to be inside", ErrEscapesRoot, candidate)
	}
	for _, root := range roots {
		if err := within(root, candidate); err == nil {
			return nil
		}
	}
	return fmt.Errorf("%w: %s is not inside any of: %s", ErrEscapesRoot, candidate, strings.Join(roots, ", "))
}

// safeBase is the filename a staged copy gets.
//
// Derived from the source rather than taken from it: a user's file name reaches
// this from a request body, and it is about to become a path element. Anything
// that is not a plain name becomes one.
func safeBase(source string) string {
	base := filepath.Base(filepath.FromSlash(source))
	base = strings.TrimSpace(base)
	base = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.' || r == '-' || r == '_':
			return r
		}
		return '_'
	}, base)
	base = strings.TrimLeft(base, ".")
	if base == "" {
		return "input"
	}
	if len(base) > 96 {
		base = base[len(base)-96:]
	}
	return base
}

// maxInputBytes bounds one staged input. A build input is a text map source or
// a BSP; a gigabyte of it is a mistake or an attempt to fill the user's disk
// through an API that was only ever asked to copy a file.
const maxInputBytes = 2 << 30

// plannedInput works out where one of the user's files will be staged, and
// refuses everything that must not be staged at all.
//
// Separate from the copy so that a command preview resolves against exactly the
// path the job will use. A preview whose `{input.source_map}` said something
// different from what runs would be a preview worth nothing.
//
// allowedRoots is the job's declared roots. When it is empty — a machine with
// no binding, which is every machine before a tool has been acquired — the
// user's own files are not restricted: they are the user's files, and the
// caller is already running as them. When roots *are* declared, a source
// outside them is refused, including one reached through a symlink, which is
// what stops an API request from staging a file the profile was never granted.
func plannedInput(l layout, name, source string, allowedRoots []string) (resolved, destination string, err error) {
	if strings.TrimSpace(source) == "" {
		return "", "", fmt.Errorf("job: the input %q has no path", name)
	}
	absolute, err := filepath.Abs(source)
	if err != nil {
		return "", "", fmt.Errorf("job: the input %q: %w", name, err)
	}
	resolved, err = resolveExisting(absolute)
	if err != nil {
		return "", "", err
	}
	if len(allowedRoots) > 0 {
		if err := withinAny(allowedRoots, resolved); err != nil {
			return "", "", fmt.Errorf("job: the input %q: %w", name, err)
		}
	}

	info, err := os.Lstat(resolved)
	if err != nil {
		return "", "", fmt.Errorf("job: the input %q: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		// Directories, devices, FIFOs and sockets. A FIFO in particular would
		// make the copy below block forever on a file the user chose.
		return "", "", fmt.Errorf("job: the input %q is %s, which is not a regular file", name, describeMode(info.Mode()))
	}
	if info.Size() > maxInputBytes {
		return "", "", fmt.Errorf("job: the input %q is %d bytes, over the %d-byte limit", name, info.Size(), int64(maxInputBytes))
	}

	destination = filepath.Join(l.Input, safeBase(name), safeBase(resolved))
	if err := within(l.Workspace, destination); err != nil {
		return "", "", fmt.Errorf("job: the input %q: %w", name, err)
	}
	return resolved, destination, nil
}

// stageInput copies one of the user's files into the job workspace.
//
// Copied, not linked and not referenced in place. A hard link would make the
// tool's in-place rewrite — which is what vis and light do — silently modify
// the user's original, and referencing it in place would do the same thing more
// obviously. The copy is the reason a failed job cannot damage a source file.
func stageInput(name, resolved, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("job: creating %s: %w", filepath.Dir(destination), err)
	}
	if err := copyFile(resolved, destination, 0o600); err != nil {
		return fmt.Errorf("job: staging the input %q: %w", name, err)
	}
	return nil
}

func describeMode(mode fs.FileMode) string {
	switch {
	case mode.IsDir():
		return "a directory"
	case mode&fs.ModeSymlink != 0:
		return "a symbolic link"
	case mode&fs.ModeNamedPipe != 0:
		return "a named pipe"
	case mode&fs.ModeSocket != 0:
		return "a socket"
	case mode&fs.ModeDevice != 0:
		return "a device"
	}
	return "not a regular file"
}

func copyFile(source, destination string, mode fs.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_EXCL, mode)
	if err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		// A second input whose file has the same name. Replacing it would make
		// one input silently become another, so the existing file is removed
		// only when it is the job's own earlier copy — which, inside a
		// freshly created per-input directory, it always is.
		if err := os.Remove(destination); err != nil {
			return err
		}
		out, err = os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_EXCL, mode)
		if err != nil {
			return err
		}
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// collect gathers a resolved action's declared outputs out of the workspace and
// publishes them.
//
// Publication is atomic as a set: everything is assembled under a temporary
// name and one rename makes the whole artifact directory appear. A reader can
// never see half of a job's results, which matters because the next stage of a
// pipeline is going to look for exactly the files the previous one declared.
func collect(l layout, outputs map[string]string, roles map[string]string, optional map[string]bool) ([]Artifact, error) {
	staging := l.Artifacts + ".staging"
	if err := os.RemoveAll(staging); err != nil {
		return nil, fmt.Errorf("job: preparing %s: %w", staging, err)
	}
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return nil, fmt.Errorf("job: creating %s: %w", staging, err)
	}
	defer os.RemoveAll(staging) // No-op after the rename below.

	artifacts := make([]Artifact, 0, len(outputs))
	var missingRequired []string
	for _, name := range sortedKeys(outputs) {
		artifact := Artifact{Name: name, Role: roles[name], Optional: optional[name]}
		produced := outputs[name]

		// The containment check that matters: the tool has run, and this is the
		// first look at what it actually left behind. A path that was inside
		// the workspace when it was resolved can be a symlink out of it now.
		if err := within(l.Workspace, produced); err != nil {
			return nil, fmt.Errorf("job: the output %q: %w", name, err)
		}
		info, err := os.Lstat(produced)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			artifact.Missing = true
			if !artifact.Optional {
				missingRequired = append(missingRequired, name)
			}
			artifacts = append(artifacts, artifact)
			continue
		case err != nil:
			return nil, fmt.Errorf("job: the output %q: %w", name, err)
		case info.Mode()&fs.ModeSymlink != 0:
			// Refused rather than followed. A symlink here is either a tool
			// doing something surprising or a document trying to publish a file
			// it was never given access to, and following it would make the
			// Companion the thing that copied it out.
			return nil, fmt.Errorf("job: the output %q is a symbolic link, which is not published: %s", name, produced)
		case !info.Mode().IsRegular():
			return nil, fmt.Errorf("job: the output %q is %s", name, describeMode(info.Mode()))
		}

		destinationDir := filepath.Join(staging, safeBase(name))
		if err := os.MkdirAll(destinationDir, 0o700); err != nil {
			return nil, fmt.Errorf("job: creating %s: %w", destinationDir, err)
		}
		destination := filepath.Join(destinationDir, safeBase(produced))
		if err := copyFile(produced, destination, 0o600); err != nil {
			return nil, fmt.Errorf("job: publishing the output %q: %w", name, err)
		}
		digest, size, err := digestFile(destination)
		if err != nil {
			return nil, err
		}
		artifact.Path = filepath.Join(l.Artifacts, safeBase(name), safeBase(produced))
		artifact.Size = size
		artifact.SHA256 = digest
		artifacts = append(artifacts, artifact)
	}

	if err := os.RemoveAll(l.Artifacts); err != nil {
		return nil, fmt.Errorf("job: replacing %s: %w", l.Artifacts, err)
	}
	if err := os.Rename(staging, l.Artifacts); err != nil {
		return nil, fmt.Errorf("job: publishing artifacts: %w", err)
	}
	if len(missingRequired) > 0 {
		return artifacts, fmt.Errorf("job: the action declared outputs it did not produce: %s", strings.Join(missingRequired, ", "))
	}
	return artifacts, nil
}

func digestFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("job: reading %s: %w", path, err)
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, fmt.Errorf("job: reading %s: %w", path, err)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), size, nil
}
