package leakadapter

import (
	"errors"
	"math"
	"strconv"
	"strings"
)

// The bounds the editor's own point file reader has; a file it would refuse is
// not a route here either.
const (
	MaxPointfileBytes  = 2 << 20
	MaxPointfilePoints = 50_000
)

// CountPoints checks a point file — EricW `.pts` or Q3Map2 `.lin`, which share
// one grammar: a finite `X Y Z` triple per line, blank lines allowed — and
// says how many points it holds. Fewer than two is not a route.
func CountPoints(text string) (int, error) {
	if len(text) > MaxPointfileBytes {
		return 0, errors.New("the point file is larger than its limit")
	}
	count := 0
	for _, line := range strings.Split(strings.TrimPrefix(text, "\xef\xbb\xbf"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return 0, errors.New("a point file line is not three numbers")
		}
		for _, field := range fields {
			value, err := strconv.ParseFloat(field, 64)
			if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || strings.ContainsAny(field, "xXpPnNiI_") {
				return 0, errors.New("a point file coordinate is not a finite number")
			}
		}
		count++
		if count > MaxPointfilePoints {
			return 0, errors.New("the point file holds more points than its limit")
		}
	}
	if count < 2 {
		return 0, errors.New("the point file holds fewer than two points")
	}
	return count, nil
}
