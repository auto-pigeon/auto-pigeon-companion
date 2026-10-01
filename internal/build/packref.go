package build

import (
	"fmt"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/pack"
)

// PackageRefs is a finished build as a package manifest records it: which build
// it was, which pipeline and tools produced it, and the digest of each output —
// the evidence a packaging policy recognises a compiled file by, wherever the
// file now sits.
//
// One function, because two commands package a build (`package create --build`
// and the Quake III map package) and a package manifest that named a build two
// different ways would be two records of one fact.
func PackageRefs(manifest *Manifest) (*pack.BuildRef, []pack.ToolRef, map[string]string) {
	digests := map[string]string{}
	for _, output := range manifest.Outputs {
		if output.Missing || output.Path == "" || output.SHA256 == "" {
			continue
		}
		digests[output.SHA256] = fmt.Sprintf("output %q of build %s", output.Name, manifest.BuildID)
	}
	ref := &pack.BuildRef{
		BuildID: manifest.BuildID,
		Pipeline: pack.DocumentRef{
			ID: manifest.Pipeline.ID, Version: manifest.Pipeline.Version,
			Name: manifest.Pipeline.Name, Digest: manifest.Pipeline.Digest,
		},
		ReproducibleKey: manifest.ReproducibleKey,
		Platform:        manifest.Platform,
	}
	var tools []pack.ToolRef
	for _, tool := range manifest.Tools {
		record := pack.ToolRef{
			Profile: pack.DocumentRef{
				ID: tool.Profile.ID, Version: tool.Profile.Version,
				Name: tool.Profile.Name, Digest: tool.Profile.Digest,
			},
			ToolVersion: tool.ToolVersion,
		}
		for _, exe := range tool.Executables {
			record.Executables = append(record.Executables, pack.ExecutableRef{Name: exe.Name, SHA256: exe.SHA256})
		}
		tools = append(tools, record)
	}
	return ref, tools, digests
}
