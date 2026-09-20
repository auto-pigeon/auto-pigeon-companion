package texturebundle

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
)

// digestFile hashes a file on disk. One implementation, because a second one
// that read the file differently would be a second answer to "are these the
// bytes that were verified".
func digestFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()

	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}

	return hex.EncodeToString(hash.Sum(nil)), size, nil
}
