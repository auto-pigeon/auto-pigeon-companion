package profile

import (
	"strings"
	"testing"
)

// `fatal` fails a job that exited 0, so it is allowed only where the finding is
// an error, and never on the line that proves the opposite.
func TestAFatalRuleIsAnErrorAndNotACleanStop(t *testing.T) {
	problems := func(rule DiagnosticRule) string {
		c := &collector{}
		rule.validate(c)
		return c.problems.Error()
	}
	base := DiagnosticRule{ID: "model_missing", Match: "ERROR: Unable to open file", Severity: SeverityError, Fatal: true, Class: "model_missing"}
	if c := (&collector{}); func() bool { base.validate(c); return len(c.problems) > 0 }() {
		t.Fatalf("a fatal error rule with a class was refused: %v", c.problems)
	}
	warning := base
	warning.Severity = SeverityWarning
	if got := problems(warning); !strings.Contains(got, "a fatal finding is an error") {
		t.Errorf("a fatal warning: %s", got)
	}
	both := base
	both.Severity, both.CleanStop = SeverityWarning, true
	if got := problems(both); !strings.Contains(got, "cannot prove both") {
		t.Errorf("fatal and clean_stop: %s", got)
	}
	shouting := base
	shouting.Class = "Model Missing"
	if got := problems(shouting); !strings.Contains(got, "class") {
		t.Errorf("a class that is not a token: %s", got)
	}
}

// Measured (Q3_010): `-fs_game ..` made Q3Map2 read the parent of every base
// path. `..` is made of characters a text option permits, so the option check
// refuses a value that is only dots, whatever option it arrives in.
func TestATextOptionThatIsOnlyDotsIsADirectoryNotAName(t *testing.T) {
	option := OptionSpec{Name: "mod", Type: OptionText}
	for _, value := range []string{".", "..", "..."} {
		if err := option.Check(value); err == nil || !strings.Contains(err.Error(), "names a directory") {
			t.Errorf("%q: %v", value, err)
		}
	}
	for _, value := range []string{"", "missionpack", "v1.32", "a.b", "my-mod_2"} {
		if err := option.Check(value); err != nil {
			t.Errorf("%q was refused: %v", value, err)
		}
	}
}
