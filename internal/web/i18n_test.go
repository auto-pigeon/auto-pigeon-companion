package web

import (
	"encoding/json"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every language the page offers has a locale file, the server accepts its
// code, and every translation keeps the {slots} of the English it translates —
// a slot that went missing in Italian would print a sentence with a hole in it.
func TestEveryOfferedLanguageHasADictionaryThatKeepsItsSlots(t *testing.T) {
	source, err := fs.ReadFile(assetsFS(), "i18n.js")
	if err != nil {
		t.Fatal(err)
	}
	codes := regexp.MustCompile(`code: "([a-z]{2})"`).FindAllStringSubmatch(string(source), -1)
	if len(codes) < 7 {
		t.Fatalf("i18n.js offers %d languages; AUP offers seven", len(codes))
	}
	page, err := fs.ReadFile(assetsFS(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	slot := regexp.MustCompile(`\{[a-zA-Z_.]+\}`)
	for _, match := range codes {
		code := match[1]
		if !pageLanguages[code] {
			t.Errorf("the server refuses the language %q the page offers", code)
		}
		if code == "en" {
			continue
		}
		file := "locales/" + code + ".js"
		if !strings.Contains(string(page), `src="`+file+`"`) {
			t.Errorf("index.html does not load %s", file)
		}
		raw, err := fs.ReadFile(assetsFS(), file)
		if err != nil {
			t.Errorf("%s is missing: %v", file, err)
			continue
		}
		body := string(raw)
		start := strings.Index(body, "= {")
		end := strings.LastIndex(body, "}")
		if start < 0 || end < start {
			t.Errorf("%s holds no dictionary", file)
			continue
		}
		text := strings.TrimSpace(body[start+2 : end+1])
		text = regexp.MustCompile(`,\s*\}$`).ReplaceAllString(text, "}")
		var dictionary map[string]string
		if err := json.Unmarshal([]byte(text), &dictionary); err != nil {
			t.Errorf("%s is not a JSON-shaped dictionary: %v", file, err)
			continue
		}
		for english, translated := range dictionary {
			want, got := slot.FindAllString(english, -1), slot.FindAllString(translated, -1)
			sort.Strings(want)
			sort.Strings(got)
			if strings.Join(want, ",") != strings.Join(got, ",") {
				t.Errorf("%s: %q keeps slots %v, its English %q has %v", file, translated, got, english, want)
			}
		}
	}
}

// Choosing a language is kept, and choosing Automatic again clears it — in
// the file and in what the server reports at once, not after a restart.
func TestTheLanguageChoiceIsSavedAndCanBeCleared(t *testing.T) {
	server, _ := newTestServer(t, nil)
	for _, want := range []string{"it", "", "fr"} {
		response, body := do(t, server, "PUT", "/api/v1/settings/language", `{"language":"`+want+`"}`)
		if response.StatusCode != 200 {
			t.Fatalf("PUT %q: %d %v", want, response.StatusCode, body)
		}
		got, _ := body["language"].(string)
		if got != want {
			t.Errorf("after choosing %q the server reports %q", want, got)
		}
	}
	response, _ := do(t, server, "PUT", "/api/v1/settings/language", `{"language":"xx"}`)
	if response.StatusCode != 400 {
		t.Errorf("an unknown language was accepted: %d", response.StatusCode)
	}
}
