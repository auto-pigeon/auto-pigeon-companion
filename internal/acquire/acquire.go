// Package acquire finds a profile's executables on this machine, by the three
// routes a profile may declare. It downloads nothing.
//
// # The three routes, and what each one trusts
//
//   - `user_path` — the user names a directory they already have. The user is
//     the authority, and pretending otherwise would be theatre.
//   - `system_path` — found on PATH under a name the profile declares. Whoever
//     installed it is who the machine already trusts.
//   - `already_installed` — found at a declared place under a root the user has
//     already configured, usually shipped with a game.
//
// All three are a resolution, not an acquisition, and they end in the same
// shape: the checks that are possible — the file exists, it is a regular file,
// it is executable, the path does not escape its root — are the same checks in
// every case.
//
// # There is no fourth route
//
// The Companion used to download toolchains and the extractor itself, against
// a signed catalogue. It does not any more (operator, 2026-09-23): every
// program it runs is one the person installed, or — for Auto-Pigeon Extractor —
// the one shipped beside it in the release (internal/aue). A document that
// still lists a `managed_download` route is read, and that route is never
// offered: see [profile.AcquireManagedDownload].
package acquire

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// Request is one profile's acquisition question.
type Request struct {
	// Option is the route the user chose, from the profile.
	Option profile.AcquisitionOption
	// Executables is the profile's declared executables, whose `file` members
	// resolve under whatever root the chosen route produces.
	Executables []profile.Executable
	// Platform is the machine. Zero means the running one.
	Platform profile.Platform
	// UserPath is where the user pointed, for `user_path`.
	UserPath string
	// Roots are the configured roots, for `already_installed`.
	Roots map[string]string
	// LookPath resolves a command on PATH; nil means exec.LookPath. A field so
	// a `system_path` resolution is testable without installing anything.
	LookPath func(string) (string, error)
}

// Result is where a profile's executables are on this machine.
type Result struct {
	Mode profile.AcquisitionMode
	// ToolRoot is the directory the profile's executable paths resolve under.
	// Empty for `system_path`, where each executable was found independently
	// and there is no common root.
	ToolRoot string
	// Executables maps each declared name to an absolute path.
	Executables map[string]string
	// Description is one sentence about where these came from, for the record
	// and for the user.
	Description string
}

// ErrNoDownloads is the answer to a `managed_download` route.
var ErrNoDownloads = errors.New("acquire: the Companion does not download programs; " +
	"choose the folder you installed it in, or put it on PATH")

// Resolve turns one acquisition option into executables on this machine.
func Resolve(request Request) (*Result, error) {
	platform := request.Platform
	if platform.Zero() {
		platform = CurrentPlatform()
	}
	if len(request.Executables) == 0 {
		return nil, errors.New("acquire: the profile declares no executables")
	}
	switch request.Option.Mode {
	case profile.AcquireUserPath:
		return resolveRoot(request, platform, request.UserPath, profile.AcquireUserPath,
			"a location you chose; nothing has verified it")
	case profile.AcquireSystemPath:
		return resolveSystemPath(request, platform)
	case profile.AcquireAlreadyInstalled:
		return resolveAlreadyInstalled(request, platform)
	case profile.AcquireManagedDownload:
		return nil, ErrNoDownloads
	}
	return nil, fmt.Errorf("acquire: %q is not an acquisition mode", request.Option.Mode)
}

// resolveRoot is the shared tail of every route that produces a directory: the
// profile's own executable paths are resolved under it, and each one is checked
// to be a regular file that is actually there.
func resolveRoot(request Request, platform profile.Platform, root string, mode profile.AcquisitionMode, description string) (*Result, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("acquire: %s needs a directory and none was given", mode)
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("acquire: resolving %s: %w", root, err)
	}
	executables := make(map[string]string, len(request.Executables))
	for _, declared := range request.Executables {
		relative := expandExeSuffix(declared.File, platform)
		if err := CheckRelativePath(relative); err != nil {
			return nil, fmt.Errorf("acquire: the profile's executable %q is at %q, which %w", declared.Name, relative, err)
		}
		path := filepath.Join(absolute, filepath.FromSlash(relative))
		if !withinRoot(absolute, path) {
			return nil, fmt.Errorf("acquire: the profile's executable %q resolves outside %s", declared.Name, absolute)
		}
		if err := checkExecutable(path); err != nil {
			return nil, err
		}
		executables[declared.Name] = path
	}
	return &Result{Mode: mode, ToolRoot: absolute, Executables: executables, Description: description}, nil
}

func resolveSystemPath(request Request, platform profile.Platform) (*Result, error) {
	look := request.LookPath
	if look == nil {
		look = exec.LookPath
	}
	commands := request.Option.Commands
	if len(commands) == 0 {
		return nil, errors.New("acquire: the acquisition option lists no commands to look for on PATH")
	}
	// One command per declared executable, matched by name, with the option's
	// list as the set of names that are allowed to be looked up. A profile that
	// declares three executables and one command is a profile that cannot be
	// resolved this way, and saying so is better than resolving one of three.
	allowed := make(map[string]bool, len(commands))
	for _, command := range commands {
		allowed[command] = true
	}
	executables := make(map[string]string, len(request.Executables))
	for _, declared := range request.Executables {
		name := declared.Name
		if !allowed[name] {
			return nil, fmt.Errorf("acquire: this route looks for %s on PATH and the profile also declares %q; "+
				"a PATH lookup can only find the commands the option names", strings.Join(commands, ", "), name)
		}
		path, err := look(name + platform.ExeSuffix())
		if err != nil {
			return nil, fmt.Errorf("acquire: %s is not on PATH", name+platform.ExeSuffix())
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("acquire: resolving %s: %w", path, err)
		}
		if err := checkExecutable(absolute); err != nil {
			return nil, err
		}
		executables[name] = absolute
	}
	return &Result{
		Mode:        profile.AcquireSystemPath,
		Executables: executables,
		Description: "found on PATH; whoever installed it is who this machine already trusts",
	}, nil
}

func resolveAlreadyInstalled(request Request, platform profile.Platform) (*Result, error) {
	role := request.Option.RelativeTo
	base, ok := request.Roots[role]
	if !ok || strings.TrimSpace(base) == "" {
		return nil, fmt.Errorf("acquire: this route is relative to the %s root and no %s is configured", role, role)
	}
	absoluteBase, err := filepath.Abs(base)
	if err != nil {
		return nil, fmt.Errorf("acquire: resolving %s: %w", base, err)
	}
	root := absoluteBase
	if sub := request.Option.Path; sub != "" {
		if err := CheckRelativePath(sub); err != nil {
			return nil, fmt.Errorf("acquire: the acquisition path %q %w", sub, err)
		}
		root = filepath.Join(absoluteBase, filepath.FromSlash(sub))
		if !withinRoot(absoluteBase, root) {
			return nil, fmt.Errorf("acquire: the acquisition path %q resolves outside the %s root", sub, role)
		}
	}
	return resolveRoot(request, platform, root, profile.AcquireAlreadyInstalled,
		"already on this machine, under the "+role+" you configured")
}

// expandExeSuffix applies the one placeholder a profile's executable path may
// contain. See [profile.Executable].
func expandExeSuffix(file string, platform profile.Platform) string {
	return strings.ReplaceAll(file, "{platform.exe_suffix}", platform.ExeSuffix())
}

// checkExecutable refuses anything that is not a regular file that could be
// run. It does not check the executable bit on Windows, where there is none.
func checkExecutable(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("acquire: %s is not there", path)
		}
		return fmt.Errorf("acquire: checking %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("acquire: %s is not a regular file", path)
	}
	if goos() != "windows" && info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("acquire: %s is not executable", path)
	}
	return nil
}

func withinRoot(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// CheckRelativePath validates a relative path a profile document supplies — an
// executable's `file`, an `already_installed` sub-path.
//
// Everything here is refused rather than sanitized: a document that names
// `../../bin/sh` meant that, and resolving it somewhere else would still be
// acting on a document that has already proved hostile. The error is a clause,
// so callers read "which %w" — "which escapes its root", "which is absolute".
func CheckRelativePath(name string) error {
	switch {
	case name == "":
		return errors.New("is empty")
	case strings.ContainsRune(name, 0):
		return errors.New("contains a NUL byte")
	case strings.ContainsRune(name, '\\'):
		return errors.New("contains a backslash, which is a path separator on Windows and a filename character elsewhere")
	case strings.HasPrefix(name, "/"):
		return errors.New("is absolute")
	case len(name) > 1 && name[1] == ':':
		return errors.New("names a Windows drive")
	case strings.HasPrefix(name, "~"):
		return errors.New("starts at a home directory")
	}
	for _, element := range strings.Split(name, "/") {
		switch element {
		case "":
			return errors.New("has an empty path element")
		case ".", "..":
			return errors.New("escapes its root")
		}
		if strings.HasPrefix(element, " ") || strings.HasSuffix(element, " ") ||
			strings.HasSuffix(element, ".") {
			return errors.New("has an element that Windows cannot store as written")
		}
		if reservedWindowsName(element) {
			return errors.New("names a reserved Windows device")
		}
	}
	if cleaned := path.Clean(name); cleaned != name {
		return errors.New("is not already in its simplest form, so what it names depends on who cleans it")
	}
	return nil
}

// reservedWindowsName reports the DOS device names that are still special in
// every directory on Windows.
func reservedWindowsName(element string) bool {
	base, _, _ := strings.Cut(element, ".")
	switch strings.ToUpper(base) {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	}
	return false
}
