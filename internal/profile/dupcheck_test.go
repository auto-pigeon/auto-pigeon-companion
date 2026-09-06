package profile

import (
	"strings"
	"testing"
)

// Duplicate-key detection has to be precise in both directions, because it is
// implemented by tracking the object/array nesting by hand — encoding/json's
// token stream does not say whether a string is a key or a value. A version
// that treated every string as a key would refuse any document that used the
// same word twice, which is most of them.

func TestDuplicateKeyDetectionDoesNotFireOnRepeatedValues(t *testing.T) {
	// Repeated string *values*, a repeated array element, and the same member
	// name used in several different objects. None of these is a duplicate key.
	data := []byte(`{
      "schema_version": "aucom.profile/1.0",
      "kind": "tool",
      "id": "example.repeats",
      "version": "1.0.0",
      "name": "name",
      "summary": "name",
      "publisher": {"name": "name"},
      "license": {"spdx": "MIT", "name": "name"},
      "tool_version": "1.0.0",
      "platforms": [
        {"platform": {"os": "linux", "arch": "amd64"}, "status": "supported"},
        {"platform": {"os": "windows", "arch": "amd64"}, "status": "supported"}
      ],
      "acquisition": [{"mode": "system_path", "title": "On PATH", "commands": ["a", "a"]}],
      "executables": [{"name": "main", "file": "example{platform.exe_suffix}"}],
      "actions": [{"id": "run", "title": "Run it", "executable": "main", "args": ["-x", "-x", "-x"]}]
    }`)
	if _, err := Decode(data); err != nil {
		t.Fatalf("a document with repeated values was refused:\n%v", err)
	}
}

func TestDuplicateKeyDetectionLocatesANestedDuplicate(t *testing.T) {
	data := []byte(`{
      "schema_version": "aucom.profile/1.0",
      "kind": "tool",
      "id": "example.nested",
      "version": "1.0.0",
      "name": "n",
      "summary": "s",
      "publisher": {"name": "Example"},
      "license": {"spdx": "MIT"},
      "tool_version": "1.0.0",
      "platforms": [{"platform": {"os": "linux", "arch": "amd64"}, "status": "supported"}],
      "acquisition": [{"mode": "system_path", "title": "On PATH", "commands": ["example"]}],
      "executables": [{"name": "main", "file": "example{platform.exe_suffix}"}],
      "actions": [{"id": "run", "title": "Run it", "executable": "main", "executable": "other", "args": ["--help"]}]
    }`)
	_, err := Decode(data)
	if err == nil {
		t.Fatal("a nested duplicate member was accepted")
	}
	if !strings.Contains(err.Error(), "actions[0].executable") {
		t.Errorf("the error does not locate the duplicate:\n%v", err)
	}
	if !strings.Contains(err.Error(), "cannot be digested honestly") {
		t.Errorf("the error does not explain why a duplicate matters:\n%v", err)
	}
}
