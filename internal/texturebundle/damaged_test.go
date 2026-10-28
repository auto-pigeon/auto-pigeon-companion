package texturebundle_test

import (
	"errors"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle/fixturewad"
)

// NEW_363 (NEW_360 F3). A bundle whose bytes are not what its manifest declares
// is DAMAGED — a different failure from a server that is down, with a different
// remedy — and the caller can tell so without reading the sentence. The
// sentence itself is unchanged.
func TestABundleThatDoesNotMatchItsManifestIsDamaged(t *testing.T) {
	f := declaredInstalledWAD()
	original := f.files[1].body
	f.files[1].body = fixturewad.WAD("fx_swapped", 99)
	f.files[1].digest = digestOf(original)
	size := int64(len(original))
	f.files[1].bytes = &size

	_, err := publish(t, newCache(t), f)
	if err == nil {
		t.Fatal("a bundle whose member does not hash to its manifest was accepted")
	}
	if !errors.Is(err, texturebundle.ErrDamaged) {
		t.Errorf("refusal %q is not marked damaged", err)
	}
}
