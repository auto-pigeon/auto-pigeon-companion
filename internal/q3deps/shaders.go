package q3deps

import (
	"archive/zip"
	"bufio"
	"io"
	"os"
	"sort"
	"strings"
)

// shaderDef is one shader a script defines and the images it names.
type shaderDef struct {
	// Name is the shader's own name, normalized.
	Name string
	// Script is the file that defines it: where the engine sees it, and where
	// it is on this machine.
	Script scriptRef
	// Images are the VFS paths of every image its stages name, in the order
	// they appear.
	Images []string
}

// imageKeywords are the shader keywords whose argument is an image.
//
// This is a list rather than a parser of the whole shader language on purpose:
// what a dependency scan needs from a shader is the files it pulls in, and a
// keyword this list does not know produces nothing rather than a wrong answer.
// The limits of that are stated in the report, which is what makes it a review.
var imageKeywords = map[string]bool{
	"map":              true,
	"clampmap":         true,
	"qer_editorimage":  true,
	"editorimage":      true,
	"lightimage":       true,
	"q3map_lightimage": true,
}

// notAFile are the shader arguments that name something the renderer makes up
// rather than a file it loads.
var notAFile = map[string]bool{
	"$lightmap": true, "$whiteimage": true, "$dynamic": true,
	"*white": true, "-": true, "noshader": true, "none": true,
}

// skyFaces are the six suffixes `skyParms` implies for a box name.
var skyFaces = []string{"_rt", "_lf", "_bk", "_ft", "_up", "_dn"}

// parseShaderScripts reads every script and returns what each shader defines,
// keyed by shader name.
//
// A later definition of the same name does not replace an earlier one: Quake
// III's own loader takes the first, and a scan that took the last would report
// files the engine will never open.
func parseShaderScripts(scripts []scriptRef) (map[string]shaderDef, error) {
	defs := map[string]shaderDef{}
	sorted := append([]scriptRef(nil), scripts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].VFS < sorted[j].VFS })
	for _, script := range sorted {
		reader, err := openShaderScript(script.Source)
		if err != nil {
			continue // a script that cannot be read is one this scan did not see
		}
		parsed := parseShaderScript(reader, script)
		reader.Close()
		for name, def := range parsed {
			if _, already := defs[name]; !already {
				defs[name] = def
			}
		}
	}
	return defs, nil
}

// openShaderScript opens a script that is either a file on this machine or an
// entry inside a PK3, which the index spells `archive!member`.
//
// Base game shader scripts live inside `pak0.pk3`, so a scan that could only
// read loose files would report every `common/*` shader as missing — and a
// review that is wrong about the base game is a review a user learns to click
// past.
func openShaderScript(source string) (io.ReadCloser, error) {
	archive, member, inside := strings.Cut(source, "!")
	if !inside {
		return os.Open(source)
	}
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return nil, err
	}
	entry, err := reader.Open(member)
	if err != nil {
		reader.Close()
		return nil, err
	}
	return zipEntry{ReadCloser: entry, archive: reader}, nil
}

// zipEntry closes the archive when the entry is closed.
type zipEntry struct {
	io.ReadCloser
	archive *zip.ReadCloser
}

func (z zipEntry) Close() error {
	err := z.ReadCloser.Close()
	if archiveErr := z.archive.Close(); err == nil {
		err = archiveErr
	}
	return err
}

func parseShaderScript(r io.Reader, script scriptRef) map[string]shaderDef {
	defs := map[string]shaderDef{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), maxShaderBytes)

	var (
		current *shaderDef
		depth   int
		pending string
	)
	for scanner.Scan() {
		line := scanner.Text()
		if cut := strings.Index(line, "//"); cut >= 0 {
			line = line[:cut]
		}
		for _, field := range splitBraces(line) {
			switch field {
			case "{":
				depth++
				if depth == 1 && current == nil && pending != "" {
					current = &shaderDef{Name: normalizeFile(pending), Script: script}
					pending = ""
				}
				continue
			case "}":
				depth--
				if depth <= 0 {
					depth = 0
					if current != nil {
						if _, already := defs[current.Name]; !already {
							defs[current.Name] = *current
						}
						current = nil
					}
				}
				continue
			}
			tokens := strings.Fields(field)
			if len(tokens) == 0 {
				continue
			}
			if depth == 0 {
				pending = tokens[0]
				continue
			}
			if current == nil {
				continue
			}
			current.Images = append(current.Images, imagesIn(tokens)...)
		}
	}
	return defs
}

// splitBraces breaks a line into the braces on it and the text between them, so
// that `{ map x.tga }` on one line reads the same as three lines.
func splitBraces(line string) []string {
	var out []string
	var current strings.Builder
	flush := func() {
		if text := strings.TrimSpace(current.String()); text != "" {
			out = append(out, text)
		}
		current.Reset()
	}
	for _, r := range line {
		switch r {
		case '{', '}':
			flush()
			out = append(out, string(r))
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return out
}

// imagesIn returns the images one shader directive names.
func imagesIn(tokens []string) []string {
	keyword := strings.ToLower(tokens[0])
	arguments := tokens[1:]
	switch {
	case keyword == "animmap" && len(arguments) > 1:
		// `animMap <frequency> <image> [<image> …]`.
		return keepFiles(arguments[1:])
	case keyword == "videomap" && len(arguments) > 0:
		return keepFiles(arguments[:1])
	case keyword == "skyparms" && len(arguments) > 0:
		// `skyParms <farbox> <cloudheight> <nearbox>`, and a box name stands
		// for six files.
		var out []string
		for _, box := range []string{arguments[0], last(arguments)} {
			if notAFile[strings.ToLower(box)] || box == "" {
				continue
			}
			for _, face := range skyFaces {
				out = append(out, normalizeFile(box)+face)
			}
		}
		return out
	case imageKeywords[keyword] && len(arguments) > 0:
		return keepFiles(arguments[:1])
	}
	return nil
}

func keepFiles(values []string) []string {
	var out []string
	for _, value := range values {
		if value == "" || notAFile[strings.ToLower(value)] {
			continue
		}
		out = append(out, normalizeFile(value))
	}
	return out
}

func last(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
}
