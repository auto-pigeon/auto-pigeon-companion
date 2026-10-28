package selfupdate

import (
	"os"
	"os/exec"
	"path/filepath"
)

// Start runs the Companion at exe — the one just put in place — with a fixed
// argument list the caller built from this process's own launch mode, never
// from anything a page, a link or a release note said.
func Start(exe string, args []string) (*exec.Cmd, error) {
	command := exec.Command(exe, args...)
	command.Dir = filepath.Dir(exe)
	command.Env = os.Environ()
	detach(command)
	if err := command.Start(); err != nil {
		return nil, err
	}
	return command, nil
}
