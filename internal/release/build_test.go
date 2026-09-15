package release

import "testing"

func TestOnlyTheFrozenShapeIsAProductVersion(t *testing.T) {
	for version, want := range map[string]bool{
		"1.842": true, "1.0": true, "unknown": false, "0.2.0": false,
		"1.842-dirty": false, "2.1": false, "1.": false, "": false,
	} {
		if got := IsProductVersion(version); got != want {
			t.Errorf("IsProductVersion(%q) = %v, want %v", version, got, want)
		}
	}
}

func TestTheBuildNamesItsTargetAndToolchain(t *testing.T) {
	build := ReadBuild()
	if build.Target == "" || build.GoVersion == "" {
		t.Errorf("build = %+v", build)
	}
}
