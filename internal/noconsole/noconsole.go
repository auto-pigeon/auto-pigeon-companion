// Package noconsole is the Windows policy for a child this program starts in
// the background (NEW_368): it gets no console window.
//
// A Companion started from Explorer, a protocol link or its own update has no
// console a child could share, so Windows gives every console program it
// starts — a compiler's version probe, the converter's handshake, taskkill,
// reg.exe — a console window of its own, which flashes up blank and closes.
// CREATE_NO_WINDOW starts it with a console that has no window: its standard
// output and error still go to the pipes the caller set, its exit status and
// its process group are what they were. Hiding a window after it is created
// (STARTF_USESHOWWINDOW) can still flash, and is not used.
//
// What is NOT background is left alone: a game or engine a person launched
// keeps its windows; a command a person typed keeps its terminal. And the
// flag is not added where Windows does not allow it: with DETACHED_PROCESS
// (no console at all, the self-update relaunch) or CREATE_NEW_CONSOLE.
//
// Elsewhere [Apply] does nothing: no other platform opens a window for a
// child.
package noconsole

const (
	// CreateNoWindow is CREATE_NO_WINDOW.
	CreateNoWindow uint32 = 0x08000000
	// DetachedProcess is DETACHED_PROCESS.
	DetachedProcess uint32 = 0x00000008
	// CreateNewConsole is CREATE_NEW_CONSOLE.
	CreateNewConsole uint32 = 0x00000010
)

// Flags is creation flags for a background child: existing ones kept, and
// CREATE_NO_WINDOW added unless they already decide the console otherwise.
func Flags(existing uint32) uint32 {
	if existing&(DetachedProcess|CreateNewConsole) != 0 {
		return existing
	}
	return existing | CreateNoWindow
}
