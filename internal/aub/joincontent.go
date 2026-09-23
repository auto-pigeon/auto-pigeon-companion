package aub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Join content — `AUB/AUG/AUCOM/AUT 244F`.
//
// A hosted game's runtime-ready client files for ONE build: the compiled BSP, an
// optional `.lit`, and anything its host declared redistributable. AUB stores the
// bytes and serves them to a reader only through a live lease they can see (AUB
// ADR 0028: not the map's read rule); this is the client half, and it holds no opinion about what
// is safe to stage — internal/joincontent re-checks every rule AUB applied,
// because the bytes are about to land on THIS machine.

// JoinContentSchema is the manifest contract this client is written against.
const JoinContentSchema = "aub-join-content/1.0"

// Join-content states AUB answers with.
const (
	JoinContentRequired    = "required"
	JoinContentNotRequired = "not_required"
	JoinContentUndeclared  = "undeclared"
)

// ContentNone is the registration's "nothing beyond the joiner's installation".
const ContentNone = "none"

// JoinContent is a lease's statement about join content.
type JoinContent struct {
	State           string `json:"state"`
	PackageSHA256   string `json:"package_sha256,omitempty"`
	TotalBytes      int64  `json:"total_bytes,omitempty"`
	FileCount       int    `json:"file_count,omitempty"`
	ContentIdentity string `json:"content_identity,omitempty"`
	Source          string `json:"source,omitempty"`
	// Readable is nil in a listing and set on a detail or a resolution.
	Readable *bool  `json:"readable,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// JoinContentFile is one file of a manifest.
type JoinContentFile struct {
	Role            string `json:"role"`
	Destination     string `json:"destination"`
	Bytes           int64  `json:"bytes"`
	SHA256          string `json:"sha256"`
	Redistributable bool   `json:"redistributable,omitempty"`
}

// JoinContentManifest is what a host uploads.
type JoinContentManifest struct {
	SchemaVersion string            `json:"schema_version"`
	MapID         string            `json:"map_id"`
	MapRevision   int               `json:"map_revision"`
	GameFamily    string            `json:"game_family"`
	Files         []JoinContentFile `json:"files"`
}

// Digest is the package's aggregate identity, over AUB's documented canonical
// text. Computed here too so a joiner verifies the manifest it was served names
// the package the ticket was bound to — AUB's word for it is not the proof.
func (m JoinContentManifest) Digest() string {
	files := append([]JoinContentFile(nil), m.Files...)
	sort.Slice(files, func(a, b int) bool { return files[a].Destination < files[b].Destination })
	var b strings.Builder
	b.WriteString(JoinContentSchema + "\n")
	b.WriteString("map_id=" + m.MapID + "\n")
	b.WriteString("map_revision=" + strconv.Itoa(m.MapRevision) + "\n")
	b.WriteString("game_family=" + m.GameFamily + "\n")
	for _, file := range files {
		b.WriteString("file=" + file.Role + " " + strings.ToLower(file.SHA256) + " " +
			strconv.FormatInt(file.Bytes, 10) + " " + file.Destination + "\n")
	}
	sum := sha256.Sum256([]byte(b.String()))

	return "sha256:" + hex.EncodeToString(sum[:])
}

// JoinPackage is a stored package, as AUB describes it.
type JoinPackage struct {
	SchemaVersion string            `json:"schema_version"`
	PackageSHA256 string            `json:"package_sha256"`
	MapID         string            `json:"map_id"`
	MapRevision   int               `json:"map_revision"`
	GameFamily    string            `json:"game_family"`
	TotalBytes    int64             `json:"total_bytes"`
	FileCount     int               `json:"file_count"`
	Files         []JoinContentFile `json:"files"`
	CreatedAt     time.Time         `json:"created_at"`
}

// Manifest is the package as a manifest, for digest verification.
func (p JoinPackage) Manifest() JoinContentManifest {
	return JoinContentManifest{SchemaVersion: p.SchemaVersion, MapID: p.MapID, MapRevision: p.MapRevision,
		GameFamily: p.GameFamily, Files: p.Files}
}

// GameJoinContent is a live game's join-content answer.
type GameJoinContent struct {
	SchemaVersion string      `json:"schema_version"`
	GameID        string      `json:"game_id"`
	MapRevision   int         `json:"map_revision"`
	EndpointKey   string      `json:"endpoint_key"`
	Package       JoinPackage `json:"package"`
}

// OwnJoinPackage reads one of this account's own packages by digest.
func (c *Client) OwnJoinPackage(ctx context.Context, digest string) (JoinPackage, error) {
	var out struct {
		SchemaVersion string      `json:"schema_version"`
		Package       JoinPackage `json:"package"`
	}
	query := url.Values{"package_sha256": []string{digest}}
	if err := c.do(ctx, http.MethodGet, HostedGamePrefix+"/packages", query, nil, &out); err != nil {
		return JoinPackage{}, err
	}

	return out.Package, checkHostedGameSchema(out.SchemaVersion)
}

// UploadJoinPackage uploads a package, streaming each file.
//
// `open` is asked for each manifest file in manifest order. The manifest part
// goes first, so a package AUB would refuse is refused before any bytes are sent.
func (c *Client) UploadJoinPackage(ctx context.Context, manifest JoinContentManifest,
	open func(JoinContentFile) (io.ReadCloser, error),
) (JoinPackage, bool, error) {
	raw, err := json.Marshal(manifest)
	if err != nil {
		return JoinPackage{}, false, err
	}
	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	go func() {
		part, err := form.CreateFormField("manifest")
		if err == nil {
			_, err = part.Write(raw)
		}
		for index, file := range manifest.Files {
			if err != nil {
				break
			}
			var body io.ReadCloser
			if body, err = open(file); err != nil {
				break
			}
			part, err = form.CreateFormFile("file."+strconv.Itoa(index), "file")
			if err == nil {
				_, err = io.Copy(part, body)
			}
			body.Close()
		}
		if err == nil {
			err = form.Close()
		}
		writer.CloseWithError(err)
	}()

	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + HostedGamePrefix + "/packages"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), reader)
	if err != nil {
		reader.Close()

		return JoinPackage{}, false, err
	}
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("Accept", "application/json")
	if c.token != "" {
		request.Header.Set("Authorization", c.token)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return JoinPackage{}, false, fmt.Errorf("aub: uploading the join package: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return JoinPackage{}, false, newAPIError(endpoint.Path, response)
	}
	var out struct {
		SchemaVersion string      `json:"schema_version"`
		Created       bool        `json:"created"`
		Package       JoinPackage `json:"package"`
	}
	if err = json.NewDecoder(response.Body).Decode(&out); err != nil {
		return JoinPackage{}, false, fmt.Errorf("aub: decoding the join package answer: %w", err)
	}

	return out.Package, out.Created, checkHostedGameSchema(out.SchemaVersion)
}

// GameJoinContent reads a live game's join-content manifest.
func (c *Client) GameJoinContent(ctx context.Context, gameID string) (GameJoinContent, error) {
	var out GameJoinContent
	path := HostedGamePrefix + "/" + url.PathEscape(gameID) + "/join-content"
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &out); err != nil {
		return GameJoinContent{}, err
	}

	return out, checkHostedGameSchema(out.SchemaVersion)
}

// DownloadJoinContentFile opens one join-content file. The caller verifies.
func (c *Client) DownloadJoinContentFile(ctx context.Context, gameID, destination string) (*Download, error) {
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + HostedGamePrefix + "/" + url.PathEscape(gameID) +
		"/join-content/files/" + escapePath(destination)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		request.Header.Set("Authorization", c.token)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("aub: downloading %s: %w", destination, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()

		return nil, newAPIError(endpoint.Path, response)
	}

	return &Download{
		Body:          response.Body,
		ContentLength: response.ContentLength,
		ETag:          strings.Trim(response.Header.Get("ETag"), `"`),
	}, nil
}
