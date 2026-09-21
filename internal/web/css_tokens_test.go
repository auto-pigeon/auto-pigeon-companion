package web

import (
	"os"
	"regexp"
	"testing"
)

// Every custom property the sheet reads is one it defines.
//
// The Activity drawer read `var(--panel, #fff)`, `var(--muted, #555)` and
// `var(--error, #a11)`. None of the three was ever defined, so on this
// dark-only page the fallbacks won and the drawer opened white with a
// near-invisible heading. A fallback hides exactly that mistake, so the check
// is on the name, not on whether a fallback exists (`AUCOM/AUE/AUB 246I1.1`).
func TestEveryCustomPropertyTheSheetReadsIsDefined(t *testing.T) {
	raw, err := os.ReadFile("assets/app.css")
	if err != nil {
		t.Fatal(err)
	}
	sheet := string(raw)
	defined := map[string]bool{}
	for _, match := range regexp.MustCompile(`(?m)^\s*(--[a-z0-9-]+)\s*:`).FindAllStringSubmatch(sheet, -1) {
		defined[match[1]] = true
	}
	for _, match := range regexp.MustCompile(`var\((--[a-z0-9-]+)`).FindAllStringSubmatch(sheet, -1) {
		if !defined[match[1]] {
			t.Errorf("app.css reads %s, which it never defines", match[1])
		}
	}
}
