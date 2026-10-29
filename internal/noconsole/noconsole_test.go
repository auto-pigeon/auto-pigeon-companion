package noconsole

import "testing"

func TestABackgroundChildGetsNoWindowAndKeepsItsOtherFlags(t *testing.T) {
	const newProcessGroup = 0x00000200
	for _, c := range []struct {
		name     string
		in, want uint32
	}{
		{"nothing set", 0, CreateNoWindow},
		{"a new process group is kept", newProcessGroup, newProcessGroup | CreateNoWindow},
		{"already no window", CreateNoWindow | newProcessGroup, CreateNoWindow | newProcessGroup},
		// Windows refuses or ignores CREATE_NO_WINDOW with these: left as asked.
		{"detached (the self-update relaunch)", DetachedProcess | newProcessGroup, DetachedProcess | newProcessGroup},
		{"a console of its own", CreateNewConsole, CreateNewConsole},
	} {
		if got := Flags(c.in); got != c.want {
			t.Errorf("%s: %#x, want %#x", c.name, got, c.want)
		}
	}
}
