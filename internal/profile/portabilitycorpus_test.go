package profile_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// The shared portability corpus.
//
// `testdata/portability-corpus.json` is carried byte-identically by this
// repository and by auto-pigeon-backend, whose `internal/companionprofile` has
// its own independent implementation of the same rule. Neither repository can
// read the other's tree at test time — they are separate checkouts — so the
// digest below is the drift check: an edit to either copy fails that
// repository's tests immediately instead of quietly making two implementations
// disagree.
//
// Why two implementations at all. This one is what a person sees before pressing
// Publish, and it is the reason the mistake is caught where it was made. AUB's is
// what DECIDES, because nothing about an HTTP request establishes that its sender
// is this program. Both are real boundaries; each is the last one for somebody.
const portabilityCorpusDigest = "a1b623d721498842e4839f634f0c518af1c6211f5df349297a1769aa16ca0156"

func TestTheSharedPortabilityCorpusIsAnsweredCorrectly(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "portability-corpus.json"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != portabilityCorpusDigest {
		t.Fatalf("the shared portability corpus has changed.\n"+
			"  digest = %s\n  pinned = %s\n"+
			"This file is carried byte-identically by auto-pigeon-backend. "+
			"Change both copies and both pins in one task, or neither.", got, portabilityCorpusDigest)
	}

	var corpus struct {
		Cases []struct {
			Name     string `json:"name"`
			Value    any    `json:"value"`
			Portable bool   `json:"portable"`
			Fault    string `json:"fault"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus.Cases) == 0 {
		t.Fatal("the corpus is empty")
	}
	for _, testCase := range corpus.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			err := profile.CheckPortable(testCase.Value)
			if testCase.Portable {
				if err != nil {
					t.Fatalf("refused a portable value: %v", err)
				}

				return
			}
			if err == nil {
				t.Fatal("accepted a value that names one machine")
			}
			if err.Error() == "" {
				t.Fatal("refused it without saying anything a person could act on")
			}
		})
	}
}
