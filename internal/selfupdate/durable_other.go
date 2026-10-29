//go:build !unix && !windows

package selfupdate

import "os"

func renameDurable(from, to string) error { return os.Rename(from, to) }

func syncDir(string) {}
