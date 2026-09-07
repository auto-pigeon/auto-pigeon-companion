package catalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Fetching the two signed documents.
//
// The address they come from is configuration — see [EnvCatalogURL] — because
// no component in this program compiles in where another one lives. There is no
// default and no fallback: a Companion that does not know where its catalogue
// is refuses managed downloads and says which variable to set, which is a
// problem somebody fixes in an afternoon. A compiled-in address that is wrong
// is a problem somebody reports three times.

// KeyringFileName and CatalogFileName are the two documents' names under the
// configured base address.
const (
	KeyringFileName = "keyring.json"
	CatalogFileName = "catalog.json"
)

// maxDocumentBytes bounds a fetched signed document. A catalogue is a few
// hundred kilobytes of text; four megabytes is generous and finite, and the
// bound has to exist before the signature is checked, because at that point the
// bytes are still nobody's.
const maxDocumentBytes = 4 << 20

// fetchTimeout bounds a document fetch end to end.
const fetchTimeout = 30 * time.Second

// ErrNoCatalogURL reports that no catalogue address is configured.
var ErrNoCatalogURL = errors.New(
	"no catalogue address is configured: set " + EnvCatalogURL +
		" or the \"catalog_url\" field in config.json")

// ErrOffline reports that something needed the network while the Companion was
// told not to use it.
var ErrOffline = errors.New("catalog: offline")

// Client fetches signed documents. It holds no credentials and sends none: a
// catalogue is public, signed data, and a fetcher that carried a token would be
// a fetcher whose URLs and errors had to be scrubbed of one.
type Client struct {
	// HTTP is the transport. A field so a test can hand it the client of an
	// in-process TLS fixture; nil means a plain client with a timeout.
	HTTP *http.Client
	// BaseURL is the configured catalogue address.
	BaseURL string
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: fetchTimeout}
}

// Fetch retrieves and decodes both signed documents. It does not verify them:
// verification is [Verifier]'s, and keeping them apart is what makes it
// possible to verify documents that came from a file, a test fixture or a
// mirror without a second code path.
func (c *Client) Fetch(ctx context.Context) (keyring, catalog *Signed, err error) {
	base, err := c.base()
	if err != nil {
		return nil, nil, err
	}
	keyringBytes, err := c.get(ctx, base, KeyringFileName)
	if err != nil {
		return nil, nil, err
	}
	catalogBytes, err := c.get(ctx, base, CatalogFileName)
	if err != nil {
		return nil, nil, err
	}
	keyring, err = DecodeSigned(keyringBytes)
	if err != nil {
		return nil, nil, err
	}
	catalog, err = DecodeSigned(catalogBytes)
	if err != nil {
		return nil, nil, err
	}
	return keyring, catalog, nil
}

func (c *Client) base() (*url.URL, error) {
	if strings.TrimSpace(c.BaseURL) == "" {
		return nil, ErrNoCatalogURL
	}
	parsed, err := url.Parse(strings.TrimSpace(c.BaseURL))
	if err != nil {
		return nil, fmt.Errorf("catalog: %s is not a URL", EnvCatalogURL)
	}
	switch {
	case parsed.Scheme != "https":
		return nil, fmt.Errorf("catalog: the catalogue address has the scheme %q; it must be https", parsed.Scheme)
	case parsed.Host == "":
		return nil, fmt.Errorf("catalog: the catalogue address has no host")
	case parsed.User != nil:
		return nil, fmt.Errorf("catalog: the catalogue address carries credentials; a catalogue is public signed data and needs none")
	}
	if !strings.HasSuffix(parsed.Path, "/") {
		parsed.Path += "/"
	}
	return parsed, nil
}

func (c *Client) get(ctx context.Context, base *url.URL, name string) ([]byte, error) {
	target := base.JoinPath(name)
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("catalog: building the request for %s: %w", RedactURL(target.String()), err)
	}
	response, err := c.httpClient().Do(request)
	if err != nil {
		return nil, fmt.Errorf("catalog: fetching %s: %w", RedactURL(target.String()), redactError(err))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("catalog: fetching %s: HTTP %d", RedactURL(target.String()), response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxDocumentBytes+1))
	if err != nil {
		return nil, fmt.Errorf("catalog: reading %s: %w", RedactURL(target.String()), redactError(err))
	}
	if int64(len(body)) > maxDocumentBytes {
		return nil, fmt.Errorf("catalog: %s is over the %d-byte limit for a signed document", RedactURL(target.String()), int64(maxDocumentBytes))
	}
	return body, nil
}

// RedactURL renders a URL for a log or an error with everything secret removed:
// no userinfo, no query string, no fragment.
//
// A catalogue URL carries no credential — [checkArtifactURL] refuses one that
// does — but a mirror or a CDN may still hand out pre-signed URLs whose query
// string *is* the authorization, and the moment such a URL reaches a log it has
// been disclosed to everyone who later reads that log. So the redaction is
// unconditional and applied at every point a URL becomes text: scheme, host and
// path are what a person needs to understand what was fetched, and they are all
// they get.
func RedactURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "[unparseable url]"
	}
	redacted := url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: parsed.Path}
	if parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return redacted.String() + " [query redacted]"
	}
	return redacted.String()
}

// redactError rewrites an error whose text contains a URL.
//
// net/http and net/url put the whole request URL, query string and all, into
// almost every error they produce. Wrapping one verbatim is the most common way
// a signed download URL ends up in a log file, so every error from a fetch goes
// through here.
type redactedError struct {
	text string
	err  error
}

func (e *redactedError) Error() string { return e.text }
func (e *redactedError) Unwrap() error { return e.err }

// RedactError is [redactError], exported for the artifact downloader, which
// wraps errors from the same net/http machinery and has the same obligation.
func RedactError(err error) error { return redactError(err) }

func redactError(err error) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	var rewritten []string
	for _, field := range strings.Fields(text) {
		trimmed := strings.Trim(field, `"'`)
		if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
			rewritten = append(rewritten, RedactURL(trimmed))
			continue
		}
		rewritten = append(rewritten, field)
	}
	return &redactedError{text: strings.Join(rewritten, " "), err: err}
}
