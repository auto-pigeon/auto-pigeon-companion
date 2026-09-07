package pack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinCorpusIsEmptyAndSaysSo(t *testing.T) {
	corpus := BuiltinCorpus()
	if len(corpus.Assets) != 0 {
		t.Fatalf("the built-in corpus has %d entries; this build has computed no digests and must claim none",
			len(corpus.Assets))
	}
	if !strings.Contains(corpus.Source, "deliberately") {
		t.Fatalf("the built-in corpus's source is %q, which does not say why it is empty", corpus.Source)
	}
}

func TestPolicyRefusesAKnownAssetByContent(t *testing.T) {
	content := "id software's actual bytes"
	corpus := &AssetCorpus{
		SchemaVersion: AssetCorpusSchemaVersion,
		Source:        "a test",
		Assets:        []KnownAsset{{SHA256: digestOf(content), Release: "Quake 1.06 registered, id1/pak1.pak"}},
	}
	policy := Policy{KnownAssets: corpus}

	// The rename does not help, which is the whole point of matching on
	// content: the file is called something innocuous and is refused anyway.
	decision := policy.Decide(Candidate{
		Path: "maps/my-own-work.bsp", Source: "/home/someone/mymap/out.bsp",
		Size: int64(len(content)), SHA256: digestOf(content),
	})
	if decision.Verdict != Refuse {
		t.Fatalf("verdict is %q, want %q", decision.Verdict, Refuse)
	}
	if decision.Provenance != ProvenanceKnownAsset {
		t.Fatalf("provenance is %q", decision.Provenance)
	}
	if !strings.Contains(decision.Reason, "Quake 1.06 registered") {
		t.Fatalf("reason is %q, want it to name the release", decision.Reason)
	}
	if decision.Rule != "known-asset-digest" {
		t.Fatalf("rule is %q", decision.Rule)
	}
}

func TestPolicyContentIdentityOutranksEverything(t *testing.T) {
	content := "id software's actual bytes"
	policy := Policy{
		KnownAssets: &AssetCorpus{
			SchemaVersion: AssetCorpusSchemaVersion, Source: "a test",
			Assets: []KnownAsset{{SHA256: digestOf(content), Release: "Quake, id1/pak0.pak"}},
		},
		// Every weaker piece of evidence says "include", and none of them wins.
		BuildOutputs:  map[string]string{digestOf(content): "step qbsp output map"},
		AuthoredRoots: []string{"/home/someone/mymap"},
	}
	decision := policy.Decide(Candidate{
		Path: "maps/x.bsp", Source: "/home/someone/mymap/x.bsp", SHA256: digestOf(content),
	})
	if decision.Verdict != Refuse {
		t.Fatalf("a file whose digest is a released asset's was included on the strength of where it sat: %+v", decision)
	}
}

func TestPolicyIncludesBuildOutputEvenInsideAGameDirectory(t *testing.T) {
	// Building straight into id1/maps is ordinary. The build manifest vouches
	// for the exact bytes, which is stronger evidence than the folder.
	policy := Policy{
		GameRoots:    []string{"/games/quake"},
		BuildOutputs: map[string]string{digestOf("compiled"): "build 20260906 step qbsp"},
	}
	decision := policy.Decide(Candidate{
		Path: "maps/e1m1.bsp", Source: "/games/quake/id1/maps/e1m1.bsp", SHA256: digestOf("compiled"),
	})
	if decision.Verdict != Include || decision.Provenance != ProvenanceBuilt {
		t.Fatalf("got %q/%q, want include/built: %s", decision.Verdict, decision.Provenance, decision.Reason)
	}
}

func TestPolicyHoldsGameDirectoryContentForReview(t *testing.T) {
	policy := Policy{GameRoots: []string{"/games/quake"}}
	decision := policy.Decide(Candidate{
		Path: "gfx/palette.lmp", Source: "/games/quake/id1/gfx/palette.lmp", SHA256: digestOf("x"),
	})
	if decision.Verdict != Review {
		t.Fatalf("verdict is %q, want %q", decision.Verdict, Review)
	}
	if decision.Provenance != ProvenanceGameContent {
		t.Fatalf("provenance is %q", decision.Provenance)
	}
	if !strings.Contains(decision.Reason, "not who wrote it") {
		t.Fatalf("reason is %q, want it to say what the evidence does and does not establish", decision.Reason)
	}
}

func TestPolicyIncludesTheUsersOwnTree(t *testing.T) {
	policy := Policy{AuthoredRoots: []string{"/home/someone/mymap"}}
	decision := policy.Decide(Candidate{
		Path: "textures/wall.tga", Source: "/home/someone/mymap/textures/wall.tga", SHA256: digestOf("x"),
	})
	if decision.Verdict != Include || decision.Provenance != ProvenanceAuthored {
		t.Fatalf("got %q/%q, want include/authored", decision.Verdict, decision.Provenance)
	}
}

func TestPolicyHoldsWhatItKnowsNothingAbout(t *testing.T) {
	decision := Policy{}.Decide(Candidate{Path: "x.dat", Source: "/tmp/x.dat", SHA256: digestOf("x")})
	if decision.Verdict != Review {
		t.Fatalf("verdict is %q, want %q — an unanswered question is held, not guessed at", decision.Verdict, Review)
	}
	if decision.Provenance != ProvenanceUnknown {
		t.Fatalf("provenance is %q", decision.Provenance)
	}
}

func TestHintsExplainAndDoNotDecide(t *testing.T) {
	// A file with every filename signal there is, from a directory nobody
	// declared: the hints appear, and the verdict is still the one the
	// evidence supports rather than the one the name suggests.
	decision := Policy{}.Decide(Candidate{
		Path: "pak0.pak", Source: "/games/quake/id1/pak0.pak", SHA256: digestOf("x"),
	})
	if decision.Verdict != Review || decision.Rule != "unknown-provenance" {
		t.Fatalf("a filename decided a verdict: %q by %q", decision.Verdict, decision.Rule)
	}
	if len(decision.Hints) < 2 {
		t.Fatalf("hints are %v, want the archive name and the id1 element both noted", decision.Hints)
	}
	for _, hint := range decision.Hints {
		if !strings.Contains(hint, "decides nothing") && !strings.Contains(hint, "not evidence") {
			t.Fatalf("the hint %q does not disclaim itself, which is how a hint gets read as a finding", hint)
		}
	}
	// And a declared root still wins over every hint.
	withRoot := Policy{AuthoredRoots: []string{"/games/quake/id1"}}.Decide(Candidate{
		Path: "pak0.pak", Source: "/games/quake/id1/pak0.pak", SHA256: digestOf("x"),
	})
	if withRoot.Verdict != Include {
		t.Fatalf("a declared root was overridden by a filename hint: %+v", withRoot)
	}
}

func TestAuthorizationIsRecordedWithItsReason(t *testing.T) {
	content := "id software's actual bytes"
	policy := Policy{
		KnownAssets: &AssetCorpus{
			SchemaVersion: AssetCorpusSchemaVersion, Source: "a test",
			Assets: []KnownAsset{{SHA256: digestOf(content), Release: "Quake, id1/pak0.pak"}},
		},
		Authorizations: map[string]string{"pak0.pak": "I hold a distribution licence, reference 12345"},
	}
	decision := policy.Decide(Candidate{Path: "pak0.pak", Source: "/x/pak0.pak", SHA256: digestOf(content)})
	if decision.Verdict != Include || !decision.Authorized {
		t.Fatalf("an explicit authorization did not take: %+v", decision)
	}
	if !strings.Contains(decision.Resolution, "reference 12345") {
		t.Fatalf("resolution is %q, want the reason recorded verbatim", decision.Resolution)
	}
	// The override changes the verdict and not the finding: a manifest that
	// recorded only "authorized" would have lost what was authorized.
	if decision.Provenance != ProvenanceKnownAsset {
		t.Fatalf("provenance is %q, want the identification kept", decision.Provenance)
	}
	if !strings.Contains(decision.Reason, "Quake, id1/pak0.pak") {
		t.Fatalf("reason is %q, want it to still name what this file is", decision.Reason)
	}
	if decision.Rule != "authorized-known-asset" {
		t.Fatalf("rule is %q, want it to name both halves of the decision", decision.Rule)
	}
}

func TestLoadAssetCorpusRefusesAListWithNoProvenanceOfItsOwn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "known.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":"aucom.known-assets/1.0","assets":[]}`), 0o644); err != nil {
		t.Fatalf("writing: %v", err)
	}
	_, err := LoadAssetCorpus(path)
	if err == nil {
		t.Fatal("a digest list with no `source` was accepted")
	}
	if !strings.Contains(err.Error(), "cannot be argued with") {
		t.Fatalf("error is %v", err)
	}
}

func TestLoadAssetCorpusChecksItsEntries(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"bad schema":  `{"schema_version":"aucom.known-assets/9.9","source":"x","assets":[]}`,
		"bad digest":  `{"schema_version":"aucom.known-assets/1.0","source":"x","assets":[{"sha256":"deadbeef","release":"r"}]}`,
		"no release":  `{"schema_version":"aucom.known-assets/1.0","source":"x","assets":[{"sha256":"sha256:` + strings.Repeat("a", 64) + `","release":"  "}]}`,
		"unknown key": `{"schema_version":"aucom.known-assets/1.0","source":"x","assets":[],"extra":1}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, "known.json")
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatalf("writing: %v", err)
			}
			if _, err := LoadAssetCorpus(path); err == nil {
				t.Fatal("it was accepted")
			}
		})
	}

	good := `{"schema_version":"aucom.known-assets/1.0","source":"computed from a retail CD","assets":[{"sha256":"sha256:` +
		strings.Repeat("a", 64) + `","release":"Quake 1.06, id1/pak0.pak","note":"shareware"}]}`
	path := filepath.Join(dir, "good.json")
	if err := os.WriteFile(path, []byte(good), 0o644); err != nil {
		t.Fatalf("writing: %v", err)
	}
	corpus, err := LoadAssetCorpus(path)
	if err != nil {
		t.Fatalf("a well-formed corpus was refused: %v", err)
	}
	if _, found := corpus.Lookup("sha256:" + strings.Repeat("A", 64)); !found {
		t.Fatal("Lookup is case-sensitive about hex, which is a way to miss a match")
	}
}

func TestSortDecisionsPutsAttentionFirst(t *testing.T) {
	decisions := []Decision{
		{Path: "z.txt", Verdict: Include, Provenance: ProvenanceBuilt},
		{Path: "a.txt", Verdict: Include, Provenance: ProvenanceBuilt},
		{Path: "review.txt", Verdict: Review, Provenance: ProvenanceUnknown},
		{Path: "refused.txt", Verdict: Refuse, Provenance: ProvenanceKnownAsset},
	}
	sortDecisions(decisions)
	want := []string{"refused.txt", "review.txt", "a.txt", "z.txt"}
	for i, name := range want {
		if decisions[i].Path != name {
			var got []string
			for _, d := range decisions {
				got = append(got, d.Path)
			}
			t.Fatalf("order is %v, want %v", got, want)
		}
	}
}
