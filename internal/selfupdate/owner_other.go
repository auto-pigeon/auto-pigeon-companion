//go:build !unix

package selfupdate

import "os"

// ownedByCurrentUser: on Windows a per-user folder's ACL is what keeps other
// users out, and a reparse point — the way a file there could point
// elsewhere — is already refused as "not a plain file".
func ownedByCurrentUser(os.FileInfo) bool { return true }
