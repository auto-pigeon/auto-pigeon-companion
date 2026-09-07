package engine_test

import "github.com/andrea-dintino/auto-pigeon-companion/internal/engine"

// The two lines that let launch_test.go stage into the harness's game root
// without repeating the struct literal.
func stagingFor(h *harness, mod, source string) engine.Staging {
	return engine.Staging{GameRoot: h.game, ModName: mod, Source: source}
}

func unstageFor(h *harness, mod string) ([]string, error) {
	return engine.Unstage(h.game, mod)
}
