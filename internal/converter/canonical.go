package converter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
)

// RequiredCompileBoundary is the rule the converter's `canonical-map` must
// enforce for this build (protocol 1.1, NEW_434): the one its tests ran.
const RequiredCompileBoundary = "canonical-map/1"

// CanonicalMap has the converter write the canonical copy of the map at source
// to destination, a file that must not exist yet: parsed, checked and written
// again, as every map from Auto-Pigeon is compiled (internal/build
// mapconfine.go, cmd/mapio canonical.go). It returns the worldspawn WAD entries
// the converter reduced to their file names. A refusal is returned as the
// converter's own sentence.
func CanonicalMap(ctx context.Context, runner Runner, source, destination string) ([]string, error) {
	if runner == nil {
		return nil, failure.As(failure.ConverterUnavailable, ErrNoConverter)
	}
	stdout, err := runner.Run(ctx, "canonical-map", "--input", source, "--output", destination, "--json")
	if err != nil {
		var exit *ExitError
		if errors.As(err, &exit) {
			if record, found := ReadTerminal(exit.Stderr); found {
				refused := errors.New(strings.TrimSuffix(record.Detail, "."))
				// The class is the converter's own word, never read off the
				// sentence: a refused map is a permanent answer about these
				// bytes, which a leak test reports to the editor as such
				// (NEW_515, AUCOM #44).
				switch {
				case record.Reason == "map_refused" && record.Refusal == "external_map":
					return nil, failure.As(failure.MapExternalFile, refused)
				case record.Reason == "map_refused" || record.Reason == "input_too_large":
					return nil, failure.As(failure.MapRefused, refused)
				}
				return nil, refused
			}
		}
		return nil, err
	}
	var result struct {
		Output      string   `json:"output"`
		ReducedWADs []string `json:"reduced_wads"`
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout))
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("%w: canonical-map: %v", ErrOutputNotJSON, err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: canonical-map wrote more than one document", ErrOutputNotJSON)
	}
	if result.Output != destination {
		return nil, fmt.Errorf("converter: canonical-map wrote %q, not the %q it was given", result.Output, destination)
	}
	return result.ReducedWADs, nil
}
