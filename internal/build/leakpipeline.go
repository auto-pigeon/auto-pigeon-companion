package build

import (
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakadapter"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// LeakPipeline is a pipeline document in the form leakadapter binds: its
// games and its outputs by role. One conversion, shared by the page's chooser
// and `companion build leak-pipeline`, so both call the same pipelines
// eligible (NEW_310A).
func LeakPipeline(id string, pipeline *profile.PipelineProfile) leakadapter.Pipeline {
	doc := leakadapter.Pipeline{ID: id}
	if pipeline.GameProfile != nil {
		doc.Games = append(doc.Games, pipeline.GameProfile.Slug, pipeline.GameProfile.EngineFamily)
	}
	for _, output := range pipeline.Outputs {
		doc.Outputs = append(doc.Outputs, leakadapter.PipelineOutput{Name: output.Name, Role: output.Role, From: output.From})
	}
	return doc
}
