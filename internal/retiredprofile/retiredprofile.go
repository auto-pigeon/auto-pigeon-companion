// Package retiredprofile carries what a person set on a retired built-in profile
// over to the maintained document that replaced it, once, and says what it did
// (NEW_380).
//
// # What moves and what never does
//
// Folders the game needs, arguments a person added and the Game Profile they
// chose say nothing about which compiler runs, so they move to the replacement's
// binding when it has none of its own and the replacement can take them.
//
// Programs do not move. A binding of `auto-pigeon.ericw-tools.q1` may point at
// ericw-tools 2.0.0-alpha11, at a copy of the fork, or at something else with the
// same file names; a banner or a base name does not prove which. An Auto-Pigeon
// build is known by the managed installer's verified record or by the
// replacement's own version check on a program somebody chose for it, and neither
// is this package's to fake. So a replacement nobody bound stays unbound and says
// it needs setup, and the retired binding stays in bindings.json, unchanged, as
// the record of what was there. A document in the profiles folder that still
// declares a retired id is a person's own profile and is left entirely alone.
//
// Each retired binding is handled once. The record is a file of its own,
// retired-profiles.json beside bindings.json, because bindings.json is decoded
// strictly and an older Companion must keep reading it after a rollback.
package retiredprofile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/fsshare"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
)

// SchemaVersion versions the record.
const SchemaVersion = "aucom.retired-profiles/1.0"

// FileName is the record, beside bindings.json.
const FileName = "retired-profiles.json"

// Refused is one setting that was not carried over, and why.
type Refused struct {
	What string `json:"what"`
	Why  string `json:"why"`
}

// Entry is what happened to one retired binding.
type Entry struct {
	Retired     string `json:"retired"`
	Replacement string `json:"replacement"`
	// Carried names each setting that moved: `root:game_root`, `arguments:qbsp`,
	// `stage_arguments:compile`, `game_profile`.
	Carried    []string  `json:"carried,omitempty"`
	NotCarried []Refused `json:"not_carried,omitempty"`
	// OwnPrograms is true when the retired binding named programs a person chose.
	// They were not carried, and the replacement's page says so.
	OwnPrograms bool      `json:"own_programs,omitempty"`
	At          time.Time `json:"at"`
}

// Record is the file.
type Record struct {
	SchemaVersion string  `json:"schema_version"`
	Entries       []Entry `json:"entries"`
}

// Path is the record beside a binding file.
func Path(bindingsPath string) string {
	return filepath.Join(filepath.Dir(bindingsPath), FileName)
}

// Load reads the record. A missing or unreadable one is empty: the worst that
// follows is that a retired binding is looked at again, and carrying only fills
// what is empty.
func Load(bindingsPath string) Record {
	record := Record{SchemaVersion: SchemaVersion}
	raw, err := fsshare.ReadFile(Path(bindingsPath))
	if err != nil {
		return record
	}
	var read Record
	if json.Unmarshal(raw, &read) != nil || read.SchemaVersion != SchemaVersion {
		return record
	}
	return read
}

// For is the entry about a replacement's retired predecessor, if there is one.
func (r Record) For(replacement string) (Entry, bool) {
	for _, entry := range r.Entries {
		if entry.Replacement == replacement {
			return entry, true
		}
	}
	return Entry{}, false
}

func (r Record) handled(retired string) bool {
	for _, entry := range r.Entries {
		if entry.Retired == retired {
			return true
		}
	}
	return false
}

// Apply carries every retired binding not handled yet. It returns what it did
// this time, which is nothing on every run but the first.
func Apply(bindingsPath string, catalog job.Catalog, now time.Time) ([]Entry, error) {
	record := Load(bindingsPath)
	set, err := binding.LoadFile(bindingsPath)
	if errors.Is(err, binding.ErrNoFile) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var waiting []string
	for _, id := range builtin.Retired() {
		if _, bound := set.Find(id); bound && !record.handled(id) {
			if _, own := lookup(catalog, id); !own {
				waiting = append(waiting, id)
			}
		}
	}
	if len(waiting) == 0 {
		return nil, nil
	}
	var done []Entry
	if _, err := binding.Update(bindingsPath, func(current *binding.Set) error {
		done = done[:0]
		for _, id := range waiting {
			entry, err := carry(current, catalog, id, now)
			if err != nil {
				return err
			}
			done = append(done, entry)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	record.Entries = append(record.Entries, done...)
	sort.Slice(record.Entries, func(i, j int) bool { return record.Entries[i].Retired < record.Entries[j].Retired })
	if err := save(bindingsPath, record); err != nil {
		return nil, err
	}
	return done, nil
}

func lookup(catalog job.Catalog, id string) (job.CatalogEntry, bool) {
	entry, err := catalog.Lookup(id)
	return entry, err == nil
}

// carry fills the replacement's binding from one retired binding.
func carry(set *binding.Set, catalog job.Catalog, id string, now time.Time) (Entry, error) {
	old, _ := set.Find(id)
	replacement, _ := builtin.ReplacementFor(id)
	result := Entry{Retired: id, Replacement: replacement, At: now,
		OwnPrograms: len(old.Executables) > 0 && old.Acquisition != profile.AcquireManagedOfficial}
	target, ok := lookup(catalog, replacement)
	if !ok {
		result.NotCarried = append(result.NotCarried, Refused{What: "everything", Why: replacement + " is not in the catalogue"})
		return result, nil
	}
	local, found := set.Find(replacement)
	if !found {
		meta := target.Profile.Metadata()
		local = binding.LocalBinding{ProfileID: meta.ID, ProfileVersion: meta.Version, ProfileDigest: target.Digest, Trust: target.Trust}
	}

	for _, role := range sortedKeys(old.Roots) {
		switch {
		// Where a program was unpacked is about that program.
		case role == profile.RootToolInstall:
		case local.Roots[role] != "":
			result.NotCarried = append(result.NotCarried, Refused{What: "root:" + role, Why: "the replacement already has its own"})
		default:
			if local.Roots == nil {
				local.Roots = map[string]string{}
			}
			local.Roots[role] = old.Roots[role]
			result.Carried = append(result.Carried, "root:"+role)
		}
	}
	if local.GameProfileID == "" && old.GameProfileID != "" {
		local.GameProfileID = old.GameProfileID
		result.Carried = append(result.Carried, "game_profile")
	}
	if tool, isTool := target.Profile.(*profile.ToolProfile); isTool {
		for _, program := range sortedKeys(old.Arguments) {
			what := "arguments:" + program
			if len(local.Arguments[program]) > 0 {
				result.NotCarried = append(result.NotCarried, Refused{What: what, Why: "the replacement already has its own"})
				continue
			}
			if err := toolTakes(tool, program, old.Arguments[program]); err != nil {
				result.NotCarried = append(result.NotCarried, Refused{What: what, Why: err.Error()})
				continue
			}
			if local.Arguments == nil {
				local.Arguments = map[string][]string{}
			}
			local.Arguments[program] = append([]string(nil), old.Arguments[program]...)
			result.Carried = append(result.Carried, what)
		}
	}
	if pipeline, isPipeline := target.Profile.(*profile.PipelineProfile); isPipeline {
		for _, stage := range sortedKeys(old.StepArguments) {
			what := "stage_arguments:" + stage
			if len(local.StepArguments[stage]) > 0 {
				result.NotCarried = append(result.NotCarried, Refused{What: what, Why: "the replacement already has its own"})
				continue
			}
			if err := pipelineTakes(catalog, pipeline, stage, old.StepArguments[stage]); err != nil {
				result.NotCarried = append(result.NotCarried, Refused{What: what, Why: err.Error()})
				continue
			}
			if local.StepArguments == nil {
				local.StepArguments = map[string][]string{}
			}
			local.StepArguments[stage] = append([]string(nil), old.StepArguments[stage]...)
			result.Carried = append(result.Carried, what)
		}
	}
	if len(result.Carried) == 0 {
		return result, nil
	}
	local.UpdatedAt = now
	if err := set.Put(local); err != nil {
		return Entry{}, err
	}
	return result, nil
}

// toolTakes is whether a program's own arguments still make sense on the
// replacement: it runs that program, and no action already manages one of them.
func toolTakes(tool *profile.ToolProfile, program string, tokens []string) error {
	declared := false
	for _, item := range tool.Executables {
		declared = declared || item.Name == program
	}
	if !declared {
		return fmt.Errorf("%s runs no program called %s", tool.Meta.ID, program)
	}
	for _, action := range tool.Actions {
		if action.Executable != program {
			continue
		}
		if err := profile.ManagedSwitchConflict(action, tokens); err != nil {
			return err
		}
	}
	return nil
}

func pipelineTakes(catalog job.Catalog, pipeline *profile.PipelineProfile, stage string, tokens []string) error {
	for _, step := range pipeline.Steps {
		if step.ID == stage {
			return job.StageTokenConflict(catalog, step, tokens)
		}
	}
	return fmt.Errorf("%s has no stage called %s", pipeline.Meta.ID, stage)
}

func save(bindingsPath string, record Record) error {
	record.SchemaVersion = SchemaVersion
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(bindingsPath)
	temporary, err := os.CreateTemp(dir, ".retired-profiles-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(raw, '\n')); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return fsshare.Replace(temporary.Name(), Path(bindingsPath))
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
