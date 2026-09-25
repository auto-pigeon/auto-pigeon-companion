// Command icon turns the Auto-Pigeon mark into the icons the release puts on
// the Companion's executables: a Windows resource object that `go build` links
// into companion.exe, and the AppIcon.icns a macOS .app shows. Linux
// executables have no icon slot, so there is nothing to make for them.
//
// The source is the 128x128 Auto-Pigeon mark the page already embeds,
// internal/web/assets/brand/ — a byte-for-byte copy of AUP's, pinned by
// internal/web/brand_test.go (operator's choice, 2026-09-25). Everything is
// derived from it at build time, with the standard library only, so no
// generated binary is committed and no tool is fetched to make one.
//
// usage:
//
//	go run ./build/icon syso --out cmd/companion   # rsrc_windows_{amd64,arm64}.syso
//	go run ./build/icon icns --out <app>/Contents/Resources/AppIcon.icns
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
)

// sourcePath is the mark, relative to the repository root.
var sourcePath = filepath.Join("internal", "web", "assets", "brand",
	"auto-pigeon-long-tail-transparent-no-padding-128x128.png")

// windowsMachines are the Windows targets a release builds, with the COFF
// machine each one's resource object is for.
var windowsMachines = map[string]uint16{
	"amd64": machineAMD64,
	"arm64": machineARM64,
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: icon syso --out <dir> | icon icns --out <file>")
	}
	flags := flag.NewFlagSet("icon "+args[0], flag.ContinueOnError)
	out := flags.String("out", "", "where to write")
	source := flags.String("source", defaultSource(), "the mark to derive the icons from")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("--out is required")
	}
	mark, err := loadMark(*source)
	if err != nil {
		return err
	}

	switch args[0] {
	case "syso":
		images, err := renderAll(mark, windowsSizes)
		if err != nil {
			return err
		}
		for arch, machine := range windowsMachines {
			object, err := buildSyso(machine, images)
			if err != nil {
				return err
			}
			path := filepath.Join(*out, "rsrc_windows_"+arch+".syso")
			if err := os.WriteFile(path, object, 0o644); err != nil {
				return err
			}
			fmt.Println("wrote", path)
		}
	case "icns":
		images, err := renderAll(mark, icnsSizes())
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(*out, buildICNS(images), 0o644); err != nil {
			return err
		}
		fmt.Println("wrote", *out)
	default:
		return fmt.Errorf("unknown subcommand %q: want syso or icns", args[0])
	}

	return nil
}

// defaultSource is the page's mark, found from this source file's location
// (two directories up is the repository root) so `go run` works from anywhere.
func defaultSource() string {
	if _, file, _, ok := runtime.Caller(0); ok {
		root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
		if candidate := filepath.Join(root, sourcePath); fileExists(candidate) {
			return candidate
		}
	}

	return sourcePath
}

func fileExists(path string) bool {
	info, err := os.Stat(path)

	return err == nil && !info.IsDir()
}

// loadMark reads the mark and refuses one that is not square: every icon
// format here is square, and stretching the mark would be worse than failing.
func loadMark(path string) (image.Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	mark, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if b := mark.Bounds(); b.Dx() != b.Dy() {
		return nil, fmt.Errorf("%s is %dx%d; an icon source must be square", path, b.Dx(), b.Dy())
	}

	return mark, nil
}

// sized is one rendering of the mark, PNG-encoded.
type sized struct {
	size int
	png  []byte
}

func renderAll(mark image.Image, sizes []int) ([]sized, error) {
	images := make([]sized, 0, len(sizes))
	for _, size := range sizes {
		data, err := encodePNG(resize(mark, size))
		if err != nil {
			return nil, err
		}
		images = append(images, sized{size: size, png: data})
	}

	return images, nil
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	if err := encoder.Encode(&buf, img); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
