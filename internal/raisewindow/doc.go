// Package raisewindow brings a game's window in front of the browser.
//
// The Companion is a background process: the click that started the game was
// the browser's, so Windows refuses the game the foreground and it opens behind
// the page (seen live, NEW_368). Windows does let any process change the z-order
// without activating anything, so the window is raised once through the topmost
// band and straight back out. Nothing stays topmost, focus is not taken and no
// input is simulated: the person's first click lands on the game. The file
// chooser does the same for its dialog (internal/pathpick).
//
// Everywhere else this does nothing: the desktop places the window.
package raisewindow
