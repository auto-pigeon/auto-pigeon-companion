package profile

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
)

// The published schemas, and what they are for.
//
// The JSON Schema documents are the *contract*: they are what a person writing
// a profile in an editor reads, what a build script validates against in CI,
// and what a future second implementation would be written from. The Go code in
// this package is the *enforcement*: it is what actually decides whether a
// document is accepted here, and it checks things a schema cannot express —
// that a placeholder names a declared input, that an argument is not shell
// syntax, that a path stays inside its root once an option's value is in it.
//
// Two descriptions of the same thing drift. The one thing that stops it is
// `schema_test.go`, which derives each type's member set from the Go structs by
// reflection and asserts that the schema lists exactly those members, with
// exactly the same ones required. A member added to a struct and forgotten in
// the schema fails the build.
//
// The schemas are not evaluated at run time. Doing that would mean a JSON
// Schema implementation, and this repository has no third-party dependencies by
// deliberate policy — a property worth more than the small amount of duplicated
// validation it costs, since the Go side has to exist regardless.

//go:embed schema/*.json
var schemaFS embed.FS

// SchemaFile returns one published schema document by file name.
func SchemaFile(name string) ([]byte, error) {
	data, err := schemaFS.ReadFile("schema/" + name)
	if err != nil {
		return nil, fmt.Errorf("profile: no schema %q is published by this build", name)
	}
	return data, nil
}

// SchemaFiles lists the published schema documents.
func SchemaFiles() []string {
	entries, err := fs.ReadDir(schemaFS, "schema")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// SchemaFileFor names the schema document for a profile kind.
func SchemaFileFor(kind Kind) (string, error) {
	switch kind {
	case KindTool:
		return "tool-profile-1.0.schema.json", nil
	case KindEngine:
		return "engine-profile-1.0.schema.json", nil
	case KindPipeline:
		return "pipeline-profile-1.0.schema.json", nil
	}
	return "", fmt.Errorf("profile: %q is not a profile kind", kind)
}
