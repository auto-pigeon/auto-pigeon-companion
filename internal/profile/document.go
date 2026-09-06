package profile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// Reading a document that came from somebody else.
//
// Decode is the trust boundary. Everything after it is typed Go values that
// have already been checked; everything before it is bytes of unknown
// provenance, and the three things done here are the ones that cannot be done
// later:
//
//  1. **Duplicate keys are refused.** JSON says nothing useful about them and
//     implementations disagree — some take the first, some the last. A document
//     that two parsers read differently is a document whose digest means
//     nothing, because the bytes a user reviewed and the bytes a program acted
//     on are not the same document.
//  2. **Unknown members are refused, with the path.** A member this build does
//     not understand is either a typo, in which case the user needs to know
//     which one, or a member from a newer format, in which case the schema
//     version should have said so. Ignoring it silently is how a `network`
//     member that a reviewer read stops being enforced.
//  3. **Missing required members are refused, with the path.** Go's zero values
//     make an absent member indistinguishable from an empty one, which is how
//     an absent `access` becomes an empty string that no switch matches.

// Profile is what the three document kinds have in common.
type Profile interface {
	// Metadata is the identity block.
	Metadata() Meta
	// ActionList is every action the profile offers.
	ActionList() []Action
	// ActionByID finds one.
	ActionByID(id string) (Action, bool)
	// Permissions is what a user is asked to grant, most consequential first.
	Permissions() []Permission
	// Validate reports every fault in the document.
	Validate() error
}

// Decode reads a profile of any kind, dispatching on its `kind` member.
func Decode(data []byte) (Profile, error) {
	kind, err := peekKind(data)
	if err != nil {
		return nil, err
	}
	switch kind {
	case KindTool:
		return DecodeTool(data)
	case KindEngine:
		return DecodeEngine(data)
	case KindPipeline:
		return DecodePipeline(data)
	}
	kinds := make([]string, 0, len(Kinds))
	for _, k := range Kinds {
		kinds = append(kinds, string(k))
	}
	return nil, Problems{{
		Path:    "kind",
		Message: fmt.Sprintf("is %q, which is not a profile kind", kind),
		Fix:     "use one of: " + strings.Join(kinds, ", "),
	}}
}

// DecodeTool reads a tool profile.
func DecodeTool(data []byte) (*ToolProfile, error) {
	var p ToolProfile
	if err := decodeInto(data, &p); err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// DecodeEngine reads an engine profile.
func DecodeEngine(data []byte) (*EngineProfile, error) {
	var p EngineProfile
	if err := decodeInto(data, &p); err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// DecodePipeline reads a pipeline profile.
func DecodePipeline(data []byte) (*PipelineProfile, error) {
	var p PipelineProfile
	if err := decodeInto(data, &p); err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Export renders a profile for publication: validated, portable, canonical.
//
// There is no second encoder. A profile written any other way would be a
// profile whose digest depended on who wrote it out.
func Export(p Profile) ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return Canonical(p)
}

func peekKind(data []byte) (Kind, error) {
	if err := checkDuplicateKeys(data); err != nil {
		return "", err
	}
	var head struct {
		Kind Kind `json:"kind"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return "", Problems{{Message: fmt.Sprintf("is not readable JSON: %v", err)}}
	}
	if head.Kind == "" {
		return "", Problems{{Path: "kind", Message: "is missing", Fix: `say what this document is: "tool", "engine" or "pipeline"`}}
	}
	return head.Kind, nil
}

// decodeInto performs the three boundary checks and then the typed decode.
func decodeInto(data []byte, target any) error {
	if err := checkDuplicateKeys(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var tree any
	if err := decoder.Decode(&tree); err != nil {
		return Problems{{Message: fmt.Sprintf("is not readable JSON: %v", err)}}
	}
	if decoder.More() {
		return Problems{{Message: "has trailing content after the document"}}
	}

	c := root("")
	checkShape(c, tree, reflect.TypeOf(target).Elem())
	if err := c.problems.ErrorOrNil(); err != nil {
		return err
	}

	// The typed decode repeats the unknown-member check without paths. It is
	// kept because it covers the types that decode themselves — Arg's string
	// or object union — which the reflective walk above deliberately does not
	// descend into.
	strict := json.NewDecoder(bytes.NewReader(data))
	strict.DisallowUnknownFields()
	if err := strict.Decode(target); err != nil {
		return Problems{{Message: fmt.Sprintf("could not be read: %v", err)}}
	}
	return nil
}

// checkDuplicateKeys walks the token stream looking for an object that names
// the same member twice.
//
// encoding/json's token stream does not say whether a string is a key or a
// value, so the object/array nesting is tracked here and the key positions are
// derived from it. Doing it by hand is worth it: this is the one fault that a
// second decode cannot find, because by then the duplicate has already been
// resolved to whichever value Go happened to keep.
func checkDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	type frame struct {
		object    bool
		expectKey bool
		seen      map[string]bool
		lastKey   string
		index     int
	}
	var (
		problems Problems
		stack    []*frame
		path     []string
	)
	here := func() string { return strings.Join(path, "") }

	// beginValue records where the value about to be read lives; endValue pops
	// that back off and returns the enclosing object to expecting a key.
	beginValue := func() {
		if len(stack) == 0 {
			return
		}
		top := stack[len(stack)-1]
		if top.object {
			path = append(path, field(top.lastKey))
			return
		}
		path = append(path, index(top.index))
		top.index++
	}
	endValue := func() {
		if len(stack) == 0 {
			return
		}
		if len(path) > 0 {
			path = path[:len(path)-1]
		}
		if top := stack[len(stack)-1]; top.object {
			top.expectKey = true
		}
	}

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Problems{{Message: fmt.Sprintf("is not readable JSON: %v", err)}}
		}

		if len(stack) > 0 && stack[len(stack)-1].object && stack[len(stack)-1].expectKey {
			top := stack[len(stack)-1]
			if delim, ok := token.(json.Delim); ok && delim == '}' {
				stack = stack[:len(stack)-1]
				endValue()
				continue
			}
			name, _ := token.(string)
			if top.seen[name] {
				problems = append(problems, Problem{
					Path:    here() + field(name),
					Message: "is named twice in the same object",
					Fix:     "remove one; JSON parsers disagree about which value a duplicated member has, so a document they read differently cannot be digested honestly",
				})
			}
			top.seen[name] = true
			top.lastKey = name
			top.expectKey = false
			continue
		}

		if delim, ok := token.(json.Delim); ok {
			switch delim {
			case '{':
				beginValue()
				stack = append(stack, &frame{object: true, expectKey: true, seen: map[string]bool{}})
			case '[':
				beginValue()
				stack = append(stack, &frame{})
			case ']':
				stack = stack[:len(stack)-1]
				endValue()
			}
			continue
		}
		beginValue()
		endValue()
	}
	return problems.ErrorOrNil()
}

// checkShape walks a decoded tree against a Go type, reporting unknown and
// missing members with their paths.
func checkShape(c *collector, tree any, t reflect.Type) {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		// A type that decodes itself owns its own strictness; walking into it
		// would apply the wrong rules to whatever shape it accepts.
		if reflect.PointerTo(t).Implements(reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()) {
			return
		}
		object, ok := tree.(map[string]any)
		if !ok {
			c.addf("is %s, where an object is expected", jsonTypeOf(tree))
			return
		}
		fields := jsonFields(t)
		for _, name := range sortedKeys(object) {
			f, known := fields[name]
			if !known {
				c.child(field(name), func(c *collector) {
					c.fixf("the members here are: "+strings.Join(sortedKeys(fields), ", "),
						"is not a member this build understands")
				})
				continue
			}
			c.child(field(name), func(c *collector) { checkShape(c, object[name], f.Type) })
		}
		for _, name := range sortedKeys(fields) {
			f := fields[name]
			if !f.Required {
				continue
			}
			if value, present := object[name]; !present || value == nil {
				c.child(field(name), func(c *collector) { c.addf("is required and missing") })
			}
		}
	case reflect.Slice:
		if tree == nil {
			return
		}
		array, ok := tree.([]any)
		if !ok {
			c.addf("is %s, where a list is expected", jsonTypeOf(tree))
			return
		}
		for i, item := range array {
			c.child(index(i), func(c *collector) { checkShape(c, item, t.Elem()) })
		}
	case reflect.Map:
		if tree == nil {
			return
		}
		object, ok := tree.(map[string]any)
		if !ok {
			c.addf("is %s, where an object is expected", jsonTypeOf(tree))
			return
		}
		for _, name := range sortedKeys(object) {
			c.child(key(name), func(c *collector) { checkShape(c, object[name], t.Elem()) })
		}
	case reflect.String:
		if tree != nil {
			if _, ok := tree.(string); !ok {
				c.addf("is %s, where text is expected", jsonTypeOf(tree))
			}
		}
	case reflect.Bool:
		if tree != nil {
			if _, ok := tree.(bool); !ok {
				c.addf("is %s, where true or false is expected", jsonTypeOf(tree))
			}
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if tree != nil {
			number, ok := tree.(json.Number)
			if !ok {
				c.addf("is %s, where a whole number is expected", jsonTypeOf(tree))
				return
			}
			if _, err := number.Int64(); err != nil {
				c.fixf("write a whole number", "is %s, which is not a whole number", number)
			}
		}
	}
}

func jsonTypeOf(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "true or false"
	case string:
		return "text"
	case json.Number:
		return "a number"
	case []any:
		return "a list"
	case map[string]any:
		return "an object"
	}
	return "an unexpected value"
}

// jsonField describes one member as the Go types define it.
type jsonField struct {
	Name     string
	Type     reflect.Type
	Required bool
	// OmitEmpty records the `omitempty` option, which the schema check reads:
	// a member that is never emitted when empty is one the schema may leave
	// out of `required` even when Go treats it as always present.
	OmitEmpty bool
}

// jsonFields is the JSON member set of a struct type, flattening embedded
// structs the way encoding/json does.
//
// It is exported through [FieldNames] because the schema-agreement test reads
// it: the JSON Schema documents and these Go types have to describe the same
// members, and the only way that stays true is if a test derives one from the
// other rather than a person keeping two lists in step.
func jsonFields(t reflect.Type) map[string]jsonField {
	fields := map[string]jsonField{}
	var walk func(reflect.Type)
	walk = func(t reflect.Type) {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue // unexported
			}
			tag := f.Tag.Get("json")
			name, options, _ := strings.Cut(tag, ",")
			if name == "-" {
				continue
			}
			if f.Anonymous && name == "" {
				inner := f.Type
				for inner.Kind() == reflect.Ptr {
					inner = inner.Elem()
				}
				if inner.Kind() == reflect.Struct {
					walk(inner)
					continue
				}
			}
			if name == "" {
				name = f.Name
			}
			fields[name] = jsonField{
				Name:      name,
				Type:      f.Type,
				Required:  f.Tag.Get("aucom") == "required",
				OmitEmpty: strings.Contains(options, "omitempty"),
			}
		}
	}
	walk(t)
	return fields
}

// FieldNames returns the JSON member names of a profile type, with the required
// ones flagged. It exists for the schema-agreement test and for tooling that
// generates documentation from the types.
func FieldNames(v any) (all []string, required []string) {
	t := reflect.TypeOf(v)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	fields := jsonFields(t)
	for _, name := range sortedKeys(fields) {
		all = append(all, name)
		if fields[name].Required {
			required = append(required, name)
		}
	}
	return all, required
}
