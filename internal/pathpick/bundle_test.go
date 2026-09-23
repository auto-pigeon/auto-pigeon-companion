package pathpick

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestAnAppBundleIsItsDeclaredProgram(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "vkQuake.app")
	writeFile(t, filepath.Join(app, "Contents", "Info.plist"),
		"<plist><dict><key>CFBundleExecutable</key>\n  <string>vkquake</string></dict></plist>")
	writeFile(t, filepath.Join(app, "Contents", "MacOS", "vkquake"), "#!")
	writeFile(t, filepath.Join(app, "Contents", "MacOS", "helper"), "#!")

	got, err := Check(OpenFile, app)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(app, "Contents", "MacOS", "vkquake") {
		t.Errorf("got %s", got)
	}
}

func TestABinaryPlistBundleFallsBackToItsOnlyProgram(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "DarkPlaces.app")
	writeFile(t, filepath.Join(app, "Contents", "Info.plist"), "bplist00…")
	writeFile(t, filepath.Join(app, "Contents", "MacOS", "darkplaces-sdl"), "#!")
	if got, ok := BundleExecutable(app); !ok || filepath.Base(got) != "darkplaces-sdl" {
		t.Errorf("got %q %v", got, ok)
	}
	writeFile(t, filepath.Join(app, "Contents", "MacOS", "second"), "#!")
	if got, ok := BundleExecutable(app); ok {
		t.Errorf("two programs and no readable plist resolved to %q; that is a guess", got)
	}
}

func TestAFolderThatIsNotABundleIsStillRefusedAsAProgram(t *testing.T) {
	dir := t.TempDir()
	if _, err := Check(OpenFile, dir); err == nil {
		t.Error("a plain folder was accepted as a program")
	}
}
