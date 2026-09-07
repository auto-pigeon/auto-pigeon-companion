package cli

import (
	"os"
	"strings"
	"testing"
)

// `companion game`, exercised through Run.
//
// What is contested here is the two GATES, not the network: nothing is
// advertised without the preview having been read, and nothing is launched
// without the command having been shown. Both refuse BEFORE any attempt to reach
// a backend, so the answer is the same signed in or not — which is what makes
// them properties of this program rather than of a deployment.

func TestGameHostRefusesWithoutTheReview(t *testing.T) {
	env, _, stderr := testEnv(t)
	code := Run(env, []string{
		"game", "host", "--job=job1", "--map=map1", "--title=Friday",
		"--engine=quakespasm", "--endpoint=203.0.113.4:26000",
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "game preview") {
		t.Errorf("the refusal does not name the review:\n%s", stderr)
	}
	if !strings.Contains(stderr.String(), "never before") {
		t.Errorf("the refusal does not say why:\n%s", stderr)
	}
}

// An advertisement names the job serving it, so that stopping the server ends the
// listing. Without one there is nothing that could ever end it but AUB's clock.
func TestGameHostRefusesWithoutTheJobServingIt(t *testing.T) {
	env, _, stderr := testEnv(t)
	code := Run(env, []string{
		"game", "host", "--map=map1", "--title=Friday",
		"--engine=quakespasm", "--endpoint=203.0.113.4:26000", "--confirm",
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "--job") {
		t.Errorf("the refusal does not name what is missing:\n%s", stderr)
	}
}

// A port is never invented, and the refusal says what a real one looks like.
func TestGamePreviewRefusesAnAddressWithNoPort(t *testing.T) {
	env, _, stderr := testEnv(t)
	code := Run(env, []string{
		"game", "preview", "--map=map1", "--title=Friday",
		"--engine=quakespasm", "--endpoint=203.0.113.4",
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "203.0.113.4:26000") {
		t.Errorf("the refusal does not show the shape it wants:\n%s", stderr)
	}
}

// The two halves of the usage text are the two halves of the feature, and a
// person meets joining before hosting.
func TestGameUsageDistinguishesJoiningFromHosting(t *testing.T) {
	env, _, stderr := testEnv(t)
	if code := Run(env, []string{"game"}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	usage := stderr.String()
	for _, want := range []string{"join", "host", "--confirm", "--approve"} {
		if !strings.Contains(usage, want) {
			t.Errorf("the usage does not mention %q:\n%s", want, usage)
		}
	}
	if strings.Index(usage, "game join") > strings.Index(usage, "game host") {
		t.Error("hosting is listed before joining; most people join before they host")
	}
}

// The registry carries it, so `companion` with no arguments lists it.
func TestTheGameCommandIsRegistered(t *testing.T) {
	found := false
	for _, name := range Names() {
		if name == "game" {
			found = true
		}
	}
	if !found {
		t.Fatalf("`game` is not in the registry: %v", Names())
	}
}

// Nothing in this command may reach an address of its own.
//
// The source is read rather than the behaviour observed, for the reason
// `internal/hostgame`'s equivalent test gives: what is being established is the
// absence of a capability, and a behavioural test can only show that a server
// browser did not run today.
func TestTheGameCommandContactsNothingButAUB(t *testing.T) {
	raw, err := os.ReadFile("game_cmd.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, capability := range []string{
		"net.Dial", "http.Get", "http.Post", "http.NewRequest", "net.LookupHost",
	} {
		if strings.Contains(source, capability) {
			t.Fatalf("game_cmd.go uses %s. Every address this program contacts is AUB's, through "+
				"the one client; a server browser here would be a second way to find games and a "+
				"first way to contact a stranger's machine.", capability)
		}
	}
}
