package q3deps

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Status is what became of one dependency.
type Status string

const (
	// StatusPackaged: the archive about to be written carries what it needs.
	StatusPackaged Status = "packaged"
	// StatusBaseGame: the installed game has it. Not to be packaged — it is
	// somebody else's file — and not a problem.
	StatusBaseGame Status = "base_game"
	// StatusUnpackaged: it is your own file, it is on this machine, and the
	// archive does not carry it. This is the silently incomplete PK3, caught.
	StatusUnpackaged Status = "unpackaged"
	// StatusMissing: nothing anywhere has it.
	StatusMissing Status = "missing"
)

// severity orders the four so that a reference with several files takes the
// worst of them. `base_game` outranks `packaged` only in this ordering's sense
// of "more interesting to a reader"; neither needs a review.
var severity = map[Status]int{StatusPackaged: 0, StatusBaseGame: 1, StatusUnpackaged: 2, StatusMissing: 3}

// Resolution is one reference and what was found for it.
type Resolution struct {
	Reference
	Status Status `json:"status"`
	// Files are what this reference needs and where each turned out to be.
	Files []located `json:"files,omitempty"`
	// Review is why a person has to look at this one, and is empty when nobody
	// has to. It is a sentence rather than a flag because "review" with no
	// reason is a thing a user clicks past.
	Review string `json:"review,omitempty"`
}

// NeedsReview reports whether this one is why the package is being held.
func (r Resolution) NeedsReview() bool { return r.Review != "" }

// Report is the whole scan.
type Report struct {
	// Maps are the map sources that were read.
	Maps []string `json:"maps"`
	// Resolutions is every distinct reference, worst first and then by name, so
	// the thing a person has to act on is the thing they read first.
	Resolutions []Resolution `json:"resolutions"`
	// Limits are the sentences saying what this scan did not look at. They are
	// part of the report rather than documentation, because a review that
	// silently bounded itself is not one.
	Limits []string `json:"limits,omitempty"`
}

// Reviewable is every resolution a person has to look at.
func (r *Report) Reviewable() []Resolution {
	var out []Resolution
	for _, resolution := range r.Resolutions {
		if resolution.NeedsReview() {
			out = append(out, resolution)
		}
	}
	return out
}

// Counts is how many of each status, for a one-line summary.
func (r *Report) Counts() map[Status]int {
	counts := map[Status]int{}
	for _, resolution := range r.Resolutions {
		counts[resolution.Status]++
	}
	return counts
}

// Blocked is the error a package that has not been reviewed must fail with, or
// nil when there is nothing to review.
func (r *Report) Blocked() error {
	reviewable := r.Reviewable()
	if len(reviewable) == 0 {
		return nil
	}
	names := make([]string, 0, len(reviewable))
	for _, resolution := range reviewable {
		names = append(names, resolution.Name)
	}
	if len(names) > 4 {
		names = append(names[:4], fmt.Sprintf("and %d more", len(reviewable)-4))
	}
	return fmt.Errorf("%d dependency reference(s) are not accounted for: %s",
		len(reviewable), strings.Join(names, ", "))
}

// Describe is the report a person reads.
func (r *Report) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "dependencies of %s\n", strings.Join(baseNames(r.Maps), ", "))
	counts := r.Counts()
	fmt.Fprintf(&b, "  %d packaged, %d from the base game, %d not packaged, %d missing\n",
		counts[StatusPackaged], counts[StatusBaseGame], counts[StatusUnpackaged], counts[StatusMissing])
	for _, resolution := range r.Resolutions {
		fmt.Fprintf(&b, "\n  %-12s %s\n", resolution.Status, resolution.Name)
		fmt.Fprintf(&b, "               %s, %d use(s)\n", resolution.From, resolution.Count)
		if resolution.Note != "" {
			fmt.Fprintf(&b, "               note: %s\n", resolution.Note)
		}
		for _, file := range resolution.Files {
			if file.found() {
				fmt.Fprintf(&b, "               %-13s %s (%s)\n", file.Role, file.Path, file.Where)
				continue
			}
			fmt.Fprintf(&b, "               %-13s %s (not found)\n", file.Role, file.Path)
		}
		if resolution.Review != "" {
			fmt.Fprintf(&b, "               review: %s\n", resolution.Review)
		}
	}
	if len(r.Limits) > 0 {
		b.WriteString("\n  what this scan did not look at:\n")
		for _, limit := range r.Limits {
			fmt.Fprintf(&b, "    - %s\n", limit)
		}
	}
	return b.String()
}

func baseNames(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		out = append(out, filepath.Base(path))
	}
	return out
}

// Scan is where a discovery looks.
type Scan struct {
	// Members is the archive about to be written: member path to the file on
	// this machine it will be made from.
	Members map[string]string
	// ContentRoots are game directories whose contents are the user's own —
	// the directory a PK3 is being built out of, or a mod directory. Each is a
	// VFS root, so `…/mymod` rather than the directory that holds it.
	ContentRoots []string
	// GameRoots are the installed game's directories, in the same shape.
	GameRoots []string
}

// Discover reads every map and reports what it depends on.
func Discover(mapPaths []string, scan Scan) (*Report, error) {
	if len(mapPaths) == 0 {
		return nil, fmt.Errorf("q3deps: no map source to scan")
	}
	idx, err := newIndex(scan.Members, scan.ContentRoots, scan.GameRoots)
	if err != nil {
		return nil, err
	}
	contentScripts, baseScripts := idx.shaderScripts()
	shaders, err := parseShaderScripts(contentScripts)
	if err != nil {
		return nil, err
	}
	baseShaders, err := parseShaderScripts(baseScripts)
	if err != nil {
		return nil, err
	}

	found := newCollector()
	for _, mapPath := range mapPaths {
		references, err := ParseMap(mapPath)
		if err != nil {
			return nil, err
		}
		for _, reference := range references {
			found.add(reference)
		}
	}

	report := &Report{Maps: append([]string(nil), mapPaths...)}
	for _, reference := range found.all() {
		report.Resolutions = append(report.Resolutions, resolve(idx, shaders, baseShaders, reference))
	}
	sort.SliceStable(report.Resolutions, func(i, j int) bool {
		left, right := report.Resolutions[i], report.Resolutions[j]
		if severity[left.Status] != severity[right.Status] {
			return severity[left.Status] > severity[right.Status]
		}
		return left.Name < right.Name
	})
	report.Limits = limits(idx, report)
	return report, nil
}

func resolve(idx *index, shaders, baseShaders map[string]shaderDef, reference Reference) Resolution {
	resolution := Resolution{Reference: reference, Status: StatusPackaged}
	switch reference.Kind {
	case KindShader:
		resolution.Files = shaderFiles(idx, shaders, baseShaders, reference.Name)
	case KindModel:
		resolution.Files = []located{idx.find(reference.Name, "model")}
	case KindSound, KindMusic:
		resolution.Files = []located{findSound(idx, reference.Name)}
	default:
		resolution.Files = []located{idx.find(reference.Name, "file")}
	}

	worst := StatusPackaged
	for _, file := range resolution.Files {
		status := StatusMissing
		switch file.Where {
		case InArchive:
			status = StatusPackaged
		case InContent:
			status = StatusUnpackaged
		case InBaseGame:
			status = StatusBaseGame
		}
		if severity[status] > severity[worst] {
			worst = status
		}
	}
	resolution.Status = worst
	resolution.Review = reviewFor(resolution)
	return resolution
}

// shaderFiles is what a shader reference needs: the script that defines it and
// every image that script names, or — when nothing defines it — an image of
// that name.
//
// A shader the base game defines stops there. Its images are inside somebody
// else's PK3 and are not yours to package, so listing them would turn a report
// about your map into a report about id Software's.
func shaderFiles(idx *index, shaders, baseShaders map[string]shaderDef, name string) []located {
	def, defined := shaders[name]
	if !defined {
		if baseDef, inBase := baseShaders[name]; inBase {
			return []located{{
				Path:   baseDef.Script.VFS,
				Where:  InBaseGame,
				Source: baseDef.Script.Source,
				Role:   "shader script",
			}}
		}
		return []located{idx.findImage(name)}
	}
	files := []located{scriptLocation(idx, def.Script)}
	seen := map[string]bool{}
	for _, image := range def.Images {
		if seen[image] {
			continue
		}
		seen[image] = true
		files = append(files, idx.findImage(image))
	}
	return files
}

// scriptLocation reports where a shader script that was parsed will end up: in
// the archive, or only on this machine.
//
// The script is a dependency in its own right and the one most often left out —
// a PK3 with the textures and without the `.shader` renders as the untextured
// default, which looks like a missing texture and is not one.
func scriptLocation(idx *index, script scriptRef) located {
	for _, member := range sortedKeys(idx.archive) {
		if sameFile(idx.archive[member], script.Source) {
			return located{Path: member, Where: InArchive, Source: script.Source, Role: "shader script"}
		}
	}
	return located{Path: script.VFS, Where: InContent, Source: script.Source, Role: "shader script"}
}

func sameFile(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return left == right
	}
	return leftAbs == rightAbs
}

// soundExtensions are what a `noise` or `music` key may leave off.
var soundExtensions = []string{".wav", ".ogg"}

func findSound(idx *index, name string) located {
	if found := idx.find(name, "sound"); found.found() {
		return found
	}
	if filepath.Ext(name) == "" {
		for _, extension := range soundExtensions {
			if found := idx.find(name+extension, "sound"); found.found() {
				return found
			}
		}
	}
	return located{Path: name, Role: "sound"}
}

// reviewFor is the sentence that says why somebody has to look, or the empty
// string when nobody does.
func reviewFor(resolution Resolution) string {
	switch resolution.Status {
	case StatusMissing:
		var missing []string
		for _, file := range resolution.Files {
			if !file.found() {
				missing = append(missing, file.Path)
			}
		}
		return fmt.Sprintf("nothing on this machine has %s.%s %s",
			strings.Join(missing, ", "), searchedFor(resolution.Kind), consequenceOf(resolution.Kind))
	case StatusUnpackaged:
		var outside []string
		for _, file := range resolution.Files {
			if file.Where == InContent {
				outside = append(outside, file.Path)
			}
		}
		return fmt.Sprintf("this is your own content and the package does not carry it: %s. "+
			"Add it to the package, or say why it stays out.", strings.Join(outside, ", "))
	}
	if resolution.Kind == KindModel && resolution.Status != StatusMissing {
		return "a model names its own shaders inside itself, and this scan does not read model files. " +
			"Whatever textures it uses are not in this report."
	}
	return ""
}

// searchedFor names the extensions that were tried, for a reference written
// without one. Naming them is what stops "missing" reading as "this program
// looked everywhere".
func searchedFor(kind Kind) string {
	switch kind {
	case KindShader:
		return " The extensions tried were " + strings.Join(imageExtensions, ", ") + "."
	case KindSound, KindMusic:
		return " The extensions tried were " + strings.Join(soundExtensions, ", ") + "."
	}
	return ""
}

// consequenceOf says what happens if nobody acts on it, in the terms of the
// thing that is missing. One sentence for every kind would have had to be
// vague enough to be true of all of them, and a vague warning is one a person
// reads past.
func consequenceOf(kind Kind) string {
	switch kind {
	case KindShader:
		return "Q3Map2 compiles the map anyway — measured: a missing shader is a warning and exit 0 — " +
			"and the surface renders as the default texture, so nothing else will stop the package " +
			"going out incomplete."
	case KindModel:
		return "Q3Map2 prints `Unable to open file` for a model it cannot find and still exits 0, so a " +
			"package missing it looks built and is not."
	case KindSound, KindMusic:
		return "Nothing in the compile touches it: a sound is missing only when somebody plays the map."
	}
	return "Nothing else will stop the package going out incomplete."
}

// limits are the sentences about what the scan did not do. They are computed
// from what it actually met rather than printed always, so that a report about
// a map with no models does not carry a paragraph about models.
func limits(idx *index, report *Report) []string {
	var out []string
	models := false
	for _, resolution := range report.Resolutions {
		if resolution.Kind == KindModel {
			models = true
		}
	}
	if models {
		out = append(out, "what a `.md3` or `.ase` model references internally — a model is a binary "+
			"file that names its own shaders, and this reads map sources and shader scripts only")
	}
	out = append(out, "what a base-game shader pulls in; its own script is read, and anything defined "+
		"there is reported as the base game's, which is the answer that decides whether you may ship it")
	out = append(out, "anything a mod's gamecode loads by name while the map is running")
	if truncated := idx.truncatedRoots(); len(truncated) > 0 {
		out = append(out, fmt.Sprintf("the whole of %s: the scan stopped at its own limit there, so a "+
			"`missing` verdict against it may be wrong", strings.Join(truncated, ", ")))
	}
	return out
}
