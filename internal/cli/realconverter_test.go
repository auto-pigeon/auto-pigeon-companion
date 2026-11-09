package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/converter"
)

// realMapio is the converter built from this commit, once per test process.
var realMapio = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "aucom-cli-mapio-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, converter.ExecutableBase)
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	command := exec.Command("go", "build", "-o", path, "./cmd/mapio")
	command.Dir = filepath.Join("..", "..")
	if output, err := command.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%v\n%s", err, output)
	}
	return path, nil
})

// useRealConverter points this test's Companion at the converter built from
// this commit: a build of a map from Auto-Pigeon runs its `canonical-map`
// (internal/build mapconfine.go, NEW_434), so it needs one even for a `.map`.
func useRealConverter(t *testing.T) {
	t.Helper()
	path, err := realMapio()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(converter.EnvBinaryOverride, path)
}
