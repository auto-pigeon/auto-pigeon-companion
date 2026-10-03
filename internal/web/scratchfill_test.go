package web

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
)

// composeFilled is what the page does with a tested profile nobody changed:
// fill the form from it, and compose the form back.
func composeFilled(t *testing.T, id string, edit func(*scratchDocument)) (map[string]any, map[string]any) {
	t.Helper()
	entry, err := builtin.Find(id)
	if err != nil {
		t.Fatal(err)
	}
	original, err := profileTree(entry.Profile)
	if err != nil {
		t.Fatal(err)
	}
	scratch, identity := scratchFromTree(original)
	scratch.BasedOn = id
	// Through JSON, as the page sends it.
	raw, _ := json.Marshal(scratch)
	var sent scratchDocument
	if err := json.Unmarshal(raw, &sent); err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(&sent)
	}
	tree, err := scratchTree(sent)
	if err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	if err := applyBasedOn(tree, sent.BasedOn); err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	composed, err := applyComposeFields(tree, composeRequest{
		ID: id, Name: identity.Name, Version: identity.Version, Summary: identity.Summary, Description: identity.Description,
		PublisherName: identity.PublisherName, PublisherURL: identity.PublisherURL, Homepage: identity.Homepage,
		LicenseSPDX: identity.LicenseSPDX, LicenseName: identity.LicenseName,
		Runtime: identity.Runtime, EngineVersion: identity.EngineVersion,
	})
	if err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	return original, composed
}

// The proof that "start from a tested profile" loses nothing: every built-in,
// filled into the one form and composed back unchanged, is the same document —
// including everything the form has no field for.
func TestATestedProfileFilledIntoTheFormAndComposedBackIsTheSameDocument(t *testing.T) {
	entries, err := builtin.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 5 {
		t.Fatalf("only %d built-in profiles", len(entries))
	}
	for _, entry := range entries {
		id := entry.Profile.Metadata().ID
		original, composed := composeFilled(t, id, nil)
		encoded, _ := json.Marshal(composed)
		document, err := profile.Decode(encoded)
		if err != nil {
			t.Errorf("%s: composed back, it no longer validates: %v", id, err)
			continue
		}
		digest, _ := profile.Digest(document)
		if digest != entry.Digest {
			back, _ := profileTree(document)
			t.Errorf("%s: composed back, it is a different document (%s, was %s):%s", id, digest, entry.Digest, treeDifferences(original, back, ""))
		}
	}
}

// What the person changes in the form is what changes — and what they remove
// stays removed, even though the tested profile had it.
func TestEditsToAFilledFormAreKeptAndNothingRemovedComesBack(t *testing.T) {
	const id = "auto-pigeon.ericw-tools.q1"
	original, composed := composeFilled(t, id, func(scratch *scratchDocument) {
		// A fourth program and an action for it; one action dropped; one
		// argument added to the compiler.
		scratch.Executables = append(scratch.Executables, scratchExecutable{Name: "maputil", Title: "Map utility", File: "bin/maputil{platform.exe_suffix}"})
		kept := scratch.Actions[:0]
		for _, action := range scratch.Actions {
			if action.ID == "light" {
				continue
			}
			if action.Executable == "qbsp" {
				action.Args = append([]string{"-nopercent"}, action.Args...)
			}
			kept = append(kept, action)
		}
		scratch.Actions = append(kept, scratchAction{ID: "maputil", Title: "Run maputil", Executable: "maputil", Args: []string{"--version"}})
	})
	actions := map[string]map[string]any{}
	for _, action := range objects(composed, "actions") {
		actions[text(action, "id")] = action
	}
	if _, present := actions["light"]; present {
		t.Error("a removed action came back from the tested profile")
	}
	if added := actions["maputil"]; added == nil || len(added) > 6 {
		t.Errorf("the added action is %v: it should hold only what was typed", added)
	}
	var compile map[string]any
	for _, action := range actions {
		if text(action, "executable") == "qbsp" {
			compile = action
		}
	}
	if args, _ := compile["args"].([]any); len(args) == 0 || args[0] != "-nopercent" {
		t.Errorf("the added argument is not first: %v", compile["args"])
	}
	// What the form has no field for is still there on the action that stayed.
	var before map[string]any
	for _, action := range objects(original, "actions") {
		if text(action, "id") == text(compile, "id") {
			before = action
		}
	}
	for name, value := range before {
		if name == "args" {
			continue
		}
		was, _ := json.Marshal(value)
		now, _ := json.Marshal(compile[name])
		if string(was) != string(now) {
			t.Errorf("the compile action's %q changed: %v -> %v", name, value, compile[name])
		}
	}
	names := []string{}
	for _, executable := range objects(composed, "executables") {
		names = append(names, text(executable, "name"))
	}
	sort.Strings(names)
	if at := sort.SearchStrings(names, "maputil"); at >= len(names) || names[at] != "maputil" {
		t.Errorf("the added program is missing: %v", names)
	}
	encoded, _ := json.Marshal(composed)
	if _, err := profile.Decode(encoded); err != nil {
		t.Errorf("the edited document does not validate: %v", err)
	}
}

// A form that says pipeline cannot be filled from a tool, and a profile that
// is not a tested one cannot be named as the source.
func TestBasedOnNamesATestedProfileOfTheSameKind(t *testing.T) {
	tree, err := scratchTree(scratchDocument{Kind: "pipeline"})
	if err != nil {
		t.Fatal(err)
	}
	if err := applyBasedOn(tree, "auto-pigeon.ericw-tools.q1"); err == nil {
		t.Error("a pipeline form was filled from a tool")
	}
	if err := applyBasedOn(tree, "local.something"); err == nil {
		t.Error("a profile that is not built in was accepted as tested")
	}
	if err := applyBasedOn(tree, ""); err != nil {
		t.Errorf("from scratch: %v", err)
	}
}

// Every argument line the form can show is one it can read back.
func TestArgumentLinesRoundTrip(t *testing.T) {
	for _, arg := range []any{
		"-threads", "{option.threads}",
		map[string]any{"value": "-fast", "when": map[string]any{"option": "fast"}},
		map[string]any{"value": "-level", "when": map[string]any{"option": "fast", "equals": "false"}},
		map[string]any{"value": "-wadpath", "when": map[string]any{"root": "content_root"}},
		map[string]any{"value": "{input.wad}", "when": map[string]any{"input": "wad"}},
	} {
		back, err := scratchArg(scratchArgLine(arg))
		if err != nil || !reflect.DeepEqual(back, arg) {
			t.Errorf("%v became the line %q and came back as %v (%v)", arg, scratchArgLine(arg), back, err)
		}
	}
}

func treeDifferences(a, b any, path string) string {
	out := ""
	switch left := a.(type) {
	case map[string]any:
		right, ok := b.(map[string]any)
		if !ok {
			return "\n  " + path + ": was an object"
		}
		for key, value := range left {
			if _, present := right[key]; !present {
				out += "\n  " + path + "." + key + ": lost"
				continue
			}
			out += treeDifferences(value, right[key], path+"."+key)
		}
		for key := range right {
			if _, present := left[key]; !present {
				out += "\n  " + path + "." + key + ": added"
			}
		}
	case []any:
		right, ok := b.([]any)
		if !ok || len(left) != len(right) {
			return "\n  " + path + ": the list changed length or kind"
		}
		for i := range left {
			out += treeDifferences(left[i], right[i], path+"["+string(rune('0'+i%10))+"]")
		}
	default:
		if !reflect.DeepEqual(a, b) {
			encodedA, _ := json.Marshal(a)
			encodedB, _ := json.Marshal(b)
			out += "\n  " + path + ": " + string(encodedA) + " -> " + string(encodedB)
		}
	}
	return out
}
