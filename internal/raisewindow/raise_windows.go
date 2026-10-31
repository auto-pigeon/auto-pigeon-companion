//go:build windows

package raisewindow

import (
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32                   = syscall.NewLazyDLL("user32.dll")
	enumWindows              = user32.NewProc("EnumWindows")
	getWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	isWindowVisible          = user32.NewProc("IsWindowVisible")
	getWindow                = user32.NewProc("GetWindow")
	getWindowRect            = user32.NewProc("GetWindowRect")
	setWindowPos             = user32.NewProc("SetWindowPos")
)

const (
	gwOwner = 4
	// minSide tells a game's window from a splash pixel or a helper's.
	minSide = 100
	// noMoveNoSizeNoActivate is SWP_NOSIZE | SWP_NOMOVE | SWP_NOACTIVATE.
	noMoveNoSizeNoActivate = 0x13
	poll                   = 100 * time.Millisecond
)

// hwndTopmost and hwndNoTopmost are (HWND)-1 and (HWND)-2.
var hwndTopmost, hwndNoTopmost = ^uintptr(0), ^uintptr(1)

type rect struct{ left, top, right, bottom int32 }

// A callback is never freed, so there is one, and one search at a time.
var (
	search      sync.Mutex
	searchPID   uint32
	searchFound uintptr
	onWindow    = syscall.NewCallback(func(window, _ uintptr) uintptr {
		var pid uint32
		_, _, _ = getWindowThreadProcessID.Call(window, uintptr(unsafe.Pointer(&pid)))
		if pid != searchPID {
			return 1
		}
		if visible, _, _ := isWindowVisible.Call(window); visible == 0 {
			return 1
		}
		if owner, _, _ := getWindow.Call(window, gwOwner); owner != 0 {
			return 1
		}
		var box rect
		if ok, _, _ := getWindowRect.Call(window, uintptr(unsafe.Pointer(&box))); ok == 0 ||
			box.right-box.left < minSide || box.bottom-box.top < minSide {
			return 1
		}
		searchFound = window
		return 0
	})
)

func find(pid int) uintptr {
	search.Lock()
	defer search.Unlock()
	searchPID, searchFound = uint32(pid), 0
	_, _, _ = enumWindows.Call(onWindow, 0)
	return searchFound
}

// Raise waits up to wait for pid's window and raises it once, reporting whether
// it did. A process with no window, a dedicated server's, is simply not found.
func Raise(pid int, wait time.Duration) bool {
	for deadline := time.Now().Add(wait); ; time.Sleep(poll) {
		if window := find(pid); window != 0 {
			_, _, _ = setWindowPos.Call(window, hwndTopmost, 0, 0, 0, 0, noMoveNoSizeNoActivate)
			_, _, _ = setWindowPos.Call(window, hwndNoTopmost, 0, 0, 0, 0, noMoveNoSizeNoActivate)
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
	}
}
