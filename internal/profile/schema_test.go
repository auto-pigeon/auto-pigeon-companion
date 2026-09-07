package profile

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The published JSON Schema documents and the Go types are two descriptions of
// one format, and two descriptions of one thing drift. These tests are what
// stops that: the member set and the required set are derived from the Go
// structs by reflection and compared with what the schema says, so a member
// added in one place and forgotten in the other fails the build rather than
// producing a schema that quietly lies to whoever is writing a profile in an
// editor.

const commonSchema = "profile-common-1.1.schema.json"

type schemaCase struct {
	// Name is what fails.
	Name string
	// File is the schema document.
	File string
	// Pointer is the path to the object definition, empty for the root.
	Pointer []string
	// Value is a zero value of the Go type the definition describes.
	Value any
}

func schemaCases() []schemaCase {
	def := func(name string, value any) schemaCase {
		return schemaCase{Name: name, File: commonSchema, Pointer: []string{"$defs", name}, Value: value}
	}
	return []schemaCase{
		def("versionRange", VersionRange{}),
		def("platform", Platform{}),
		def("platformSupport", PlatformSupport{}),
		def("publisher", Publisher{}),
		def("source", Source{}),
		def("license", License{}),
		def("compatibility", Compatibility{}),
		def("gameProfileRef", GameProfileRef{}),
		def("meta", Meta{}),
		def("capability", Capability{}),
		def("contentLayout", ContentLayout{}),
		def("acquisitionOption", AcquisitionOption{}),
		def("versionParse", VersionParse{}),
		def("versionProbe", VersionProbe{}),
		def("executable", Executable{}),
		def("argWhen", ArgWhen{}),
		def("inputSpec", InputSpec{}),
		def("outputSpec", OutputSpec{}),
		def("enumValue", EnumValue{}),
		def("optionSpec", OptionSpec{}),
		def("diagnosticRule", DiagnosticRule{}),
		def("workingDir", WorkingDir{}),
		def("rootRef", RootRef{}),
		def("networkNeed", NetworkNeed{}),
		def("environmentPolicy", EnvironmentPolicy{}),
		def("action", Action{}),
		{Name: "toolProfile", File: "tool-profile-1.1.schema.json", Value: ToolProfile{}},
		{Name: "engineProfile", File: "engine-profile-1.1.schema.json", Value: EngineProfile{}},
		{Name: "pipelineProfile", File: "pipeline-profile-1.1.schema.json", Value: PipelineProfile{}},
		{Name: "pipelineStep", File: "pipeline-profile-1.1.schema.json", Pointer: []string{"$defs", "pipelineStep"}, Value: PipelineStep{}},
		{Name: "pipelineWire", File: "pipeline-profile-1.1.schema.json", Pointer: []string{"$defs", "pipelineWire"}, Value: PipelineWire{}},
		{Name: "pipelineOutput", File: "pipeline-profile-1.1.schema.json", Pointer: []string{"$defs", "pipelineOutput"}, Value: PipelineOutput{}},
	}
}

func loadSchema(t *testing.T, file string) map[string]any {
	t.Helper()
	data, err := SchemaFile(file)
	if err != nil {
		t.Fatalf("SchemaFile(%q): %v", file, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("%s is not valid JSON: %v", file, err)
	}
	return doc
}

func navigate(t *testing.T, doc map[string]any, pointer []string) map[string]any {
	t.Helper()
	node := doc
	for _, step := range pointer {
		next, ok := node[step].(map[string]any)
		if !ok {
			t.Fatalf("the schema has no %s", strings.Join(pointer, "/"))
		}
		node = next
	}
	return node
}

func TestSchemaAndGoTypesDescribeTheSameMembers(t *testing.T) {
	for _, c := range schemaCases() {
		t.Run(c.Name, func(t *testing.T) {
			node := navigate(t, loadSchema(t, c.File), c.Pointer)

			properties, ok := node["properties"].(map[string]any)
			if !ok {
				t.Fatalf("the definition has no `properties`")
			}
			var schemaMembers []string
			for name := range properties {
				schemaMembers = append(schemaMembers, name)
			}
			sort.Strings(schemaMembers)

			goMembers, goRequired := FieldNames(c.Value)
			if !reflect.DeepEqual(schemaMembers, goMembers) {
				t.Errorf("member sets differ\n  schema: %v\n  Go:     %v\n  only in schema: %v\n  only in Go:     %v",
					schemaMembers, goMembers, missing(schemaMembers, goMembers), missing(goMembers, schemaMembers))
			}

			var schemaRequired []string
			for _, name := range asStrings(node["required"]) {
				schemaRequired = append(schemaRequired, name)
			}
			sort.Strings(schemaRequired)
			sort.Strings(goRequired)
			if len(schemaRequired) == 0 {
				schemaRequired = nil
			}
			if len(goRequired) == 0 {
				goRequired = nil
			}
			if !reflect.DeepEqual(schemaRequired, goRequired) {
				t.Errorf("required sets differ\n  schema: %v\n  Go:     %v", schemaRequired, goRequired)
			}

			if strict, _ := node["additionalProperties"].(bool); strict {
				t.Errorf("`additionalProperties` is true; every object in this format is closed")
			} else if _, present := node["additionalProperties"]; !present {
				t.Errorf("`additionalProperties` is not set; every object in this format is closed, and a schema that omits it accepts members the Go decoder refuses")
			}
		})
	}
}

// Arg decodes itself — it is a string or an object — so the reflective walk in
// document.go deliberately does not descend into it and the case table above
// leaves it out. Its object branch still has to agree with the Go type, so it
// is checked here on its own terms.
func TestArgSchemaUnionMatchesGoType(t *testing.T) {
	node := navigate(t, loadSchema(t, commonSchema), []string{"$defs", "arg"})
	branches, ok := node["oneOf"].([]any)
	if !ok || len(branches) != 2 {
		t.Fatalf("`arg` is not a two-branch union")
	}
	object, ok := branches[1].(map[string]any)
	if !ok {
		t.Fatalf("the second branch of `arg` is not an object schema")
	}
	properties, ok := object["properties"].(map[string]any)
	if !ok {
		t.Fatalf("the object branch has no `properties`")
	}
	var members []string
	for name := range properties {
		members = append(members, name)
	}
	sort.Strings(members)

	goMembers, goRequired := FieldNames(Arg{})
	if !reflect.DeepEqual(members, goMembers) {
		t.Errorf("member sets differ\n  schema: %v\n  Go:     %v", members, goMembers)
	}
	required := asStrings(object["required"])
	sort.Strings(required)
	if !reflect.DeepEqual(required, goRequired) {
		t.Errorf("required sets differ\n  schema: %v\n  Go:     %v", required, goRequired)
	}
	if first, ok := branches[0].(map[string]any); !ok || first["type"] != "string" {
		t.Errorf("the first branch of `arg` is not the plain string spelling")
	}
}

// schemaVersionInName matches the MAJOR.MINOR a schema file name carries.
var schemaVersionInName = regexp.MustCompile(`[0-9]+\.[0-9]+`)

func TestEverySchemaFileIsValidJSONAndVersioned(t *testing.T) {
	files := SchemaFiles()
	if len(files) == 0 {
		t.Fatal("no schema documents are embedded")
	}
	for _, name := range files {
		data, err := SchemaFile(name)
		if err != nil {
			t.Fatalf("SchemaFile(%q): %v", name, err)
		}
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if doc["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
			t.Errorf("%s: does not declare the JSON Schema dialect it is written in", name)
		}
		if _, ok := doc["$id"].(string); !ok {
			t.Errorf("%s: has no $id", name)
		}
		// The file name carries the format version, and it is the version the
		// $id names. A schema file whose name and $id disagree is one a reader
		// can fetch by URL and get a different document from the one in the
		// repository.
		version := schemaVersionInName.FindString(name)
		if version == "" {
			t.Errorf("%s: the file name does not carry a schema version", name)
			continue
		}
		if id, _ := doc["$id"].(string); !strings.Contains(id, "/"+version) {
			t.Errorf("%s: the file name says %s and the $id says %q", name, version, id)
		}
	}
	for _, kind := range Kinds {
		file, err := SchemaFileFor(kind)
		if err != nil {
			t.Errorf("SchemaFileFor(%q): %v", kind, err)
			continue
		}
		if _, err := SchemaFile(file); err != nil {
			t.Errorf("SchemaFileFor(%q) names %q, which is not embedded", kind, file)
		}
	}
}

// The structural half of "a local binding is never serializable into a shared
// profile": internal/binding imports internal/profile, so nothing in
// internal/profile can name a binding type. Go enforces it by refusing the
// import cycle; this test is here so a future refactor that moves a type across
// the line fails with an explanation rather than with a cycle error.
func TestProfilePackageDoesNotImportBinding(t *testing.T) {
	set := token.NewFileSet()
	packages, err := parser.ParseDir(set, ".", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parsing this package: %v", err)
	}
	for _, pkg := range packages {
		for name, file := range pkg.Files {
			for _, imported := range file.Imports {
				path := strings.Trim(imported.Path.Value, `"`)
				if strings.HasSuffix(path, "/internal/binding") {
					t.Errorf("%s imports %s. A profile must not be able to contain a local binding: "+
						"the one-way import is the guarantee, and reversing it makes a machine path a "+
						"member of a document that gets published.", filepath.Base(name), path)
				}
			}
		}
	}
}

func asStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func missing(from, in []string) []string {
	have := map[string]bool{}
	for _, v := range in {
		have[v] = true
	}
	var out []string
	for _, v := range from {
		if !have[v] {
			out = append(out, v)
		}
	}
	return out
}

func TestSchemaFilesAreOnDiskWhereDocumentationPointsAtThem(t *testing.T) {
	entries, err := os.ReadDir("schema")
	if err != nil {
		t.Fatalf("reading schema/: %v", err)
	}
	if len(entries) != len(SchemaFiles()) {
		t.Errorf("schema/ holds %d files but %d are embedded", len(entries), len(SchemaFiles()))
	}
}
