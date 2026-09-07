package acquire

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/catalog"
)

// downloadTimeout bounds one artifact fetch end to end.
//
// End to end and not per-read, because the attack and the failure are the same
// shape: a server that sends a byte every twenty seconds keeps a per-read
// deadline satisfied forever. Ten minutes is generous for a toolchain on a slow
// link and finite for one that has stalled.
const downloadTimeout = 10 * time.Minute

// ErrDigestMismatch reports that downloaded bytes are not the bytes the
// catalogue said they would be.
//
// Never retried automatically. A mismatch is corruption or tampering, and the
// difference is not something this program can tell from here; retrying turns a
// visible incident into a loop that eventually succeeds against whichever
// server answers next.
var ErrDigestMismatch = errors.New("acquire: the download does not match its expected digest")

// ErrSizeMismatch reports a download whose length is not the declared one.
// Checked before the digest, because the size is what bounds the read: a
// digest check alone would have to consume an unbounded stream first.
var ErrSizeMismatch = errors.New("acquire: the download is not the declared size")

// Downloader fetches artifact bytes. It holds no credentials and sends none.
type Downloader struct {
	// HTTP is the transport. A field so a test can hand it the client of an
	// in-process TLS fixture.
	HTTP *http.Client
}

func (d *Downloader) client() *http.Client {
	if d.HTTP != nil {
		return d.HTTP
	}
	return &http.Client{}
}

// download fetches one artifact into path and returns nothing usable unless the
// size and the digest both matched.
//
// The order is the security property, and it is the reason this is not three
// lines of io.Copy: bytes land in a file inside a private staging directory
// that is on nobody's path, the length is enforced *while* reading rather than
// checked afterwards, the digest is computed over what was actually written,
// and only then does any caller get to open the file. A download that fails at
// any point leaves a file the caller deletes and nothing that could be run.
func (d *Downloader) download(ctx context.Context, artifact catalog.Artifact, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	where := catalog.RedactURL(artifact.URL)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return fmt.Errorf("acquire: building the request for %s: %w", where, err)
	}
	response, err := d.client().Do(request)
	if err != nil {
		return fmt.Errorf("acquire: downloading %s: %w", where, catalog.RedactError(err))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("acquire: downloading %s: HTTP %d", where, response.StatusCode)
	}
	// A declared Content-Length that disagrees with the catalogue is settled
	// here rather than after a transfer: there is no reason to spend somebody's
	// bandwidth on bytes that are already known to be the wrong ones.
	if response.ContentLength >= 0 && response.ContentLength != artifact.Size {
		return fmt.Errorf("%w: %s offers %d bytes and the catalogue says %d",
			ErrSizeMismatch, where, response.ContentLength, artifact.Size)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("acquire: creating the download file: %w", err)
	}
	hash := sha256.New()
	// Size+1 so that a server sending more than it promised is caught rather
	// than silently truncated to the right length.
	written, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, artifact.Size+1))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("acquire: downloading %s: %w", where, catalog.RedactError(err))
	}
	if written != artifact.Size {
		return fmt.Errorf("%w: %s sent %d bytes and the catalogue says %d",
			ErrSizeMismatch, where, written, artifact.Size)
	}
	got := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	if got != artifact.SHA256 {
		return fmt.Errorf("%w: %s is %s and the catalogue says %s", ErrDigestMismatch, where, got, artifact.SHA256)
	}
	return nil
}
