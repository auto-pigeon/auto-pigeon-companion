package profile

import (
	"strings"
	"testing"
)

func TestAnOptionalRootUsedWithoutAConditionIsRefused(t *testing.T) {
	document := strings.Replace(optionalRootTool, `{ "value": "{root.content_root}", "when": { "root": "content_root" } }`, `"{root.content_root}"`, 1)
	if _, err := Decode([]byte(document)); err == nil || !strings.Contains(err.Error(), "without being conditioned") {
		t.Errorf("err = %v", err)
	}
	if _, err := Decode([]byte(optionalRootTool)); err != nil {
		t.Errorf("the conditioned form was refused: %v", err)
	}
	notOptional := strings.Replace(optionalRootTool, `"optional": true`, `"optional": false`, 1)
	if _, err := Decode([]byte(notOptional)); err == nil || !strings.Contains(err.Error(), "optional root") {
		t.Errorf("a root condition on a required root was accepted: %v", err)
	}
}

const optionalRootTool = `{
  "schema_version": "aucom.profile/1.1", "kind": "tool", "id": "local.optional-root", "version": "1.0.0",
  "name": "Optional root", "summary": "A folder the tool may be given.",
  "publisher": { "name": "Tests" }, "license": { "spdx": "MIT" }, "tool_version": "1",
  "platforms": [ { "platform": { "os": "linux", "arch": "amd64" }, "status": "unverified", "note": "not run here" } ],
  "acquisition": [ { "mode": "user_path", "title": "yours", "hint": "choose the folder" } ],
  "executables": [ { "name": "cc", "file": "cc" } ],
  "actions": [ {
    "id": "go", "title": "Go", "executable": "cc",
    "args": [ { "value": "-wadpath", "when": { "root": "content_root" } }, { "value": "{root.content_root}", "when": { "root": "content_root" } } ],
    "roots": [
      { "role": "workspace", "access": "read_write", "purpose": "work" },
      { "role": "content_root", "access": "read", "optional": true, "purpose": "textures" }
    ]
  } ]
}`
