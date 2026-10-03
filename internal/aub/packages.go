package aub

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Asset packages: the PK3 archives an account holds for Quake III maps.
//
// # Why the Companion reads these (Q3_010)
//
// A saved Quake III map names the packages it is built with by DIGEST, in its
// own document. AUB holds the archives, owner-only. Until `Q3_010` a build of
// such a map compiled against whatever folder the user had bound, and nothing
// connected the two: `Q3_007` fetched the bound archive by hand and staged it by
// hand, and said so. These two calls are what lets the Companion do that itself
// — find the archive an account holds for a digest, and download it — through
// the one authenticated client, with the bytes verified by the caller against
// the digest the MAP recorded, not against anything this response says.

// AssetPackage is one package row, as far as a build needs it.
type AssetPackage struct {
	ID               string `json:"id"`
	Game             string `json:"game"`
	Root             string `json:"root"`
	ArchiveName      string `json:"archive_name"`
	ArchiveSHA256    string `json:"archive_sha256"`
	ArchiveBytes     int64  `json:"archive_bytes"`
	ArchiveAvailable bool   `json:"archive_available"`
	State            string `json:"state"`
}

// assetPackagePage is the page size asked for. AUB caps it at its own maximum.
const assetPackagePage = 100

// maxAssetPackages bounds the walk through an account's packages. AUB's own
// per-account limit is far below it; the bound exists so that a deployment
// which reported an ever-growing total could not hold a build open for ever.
const maxAssetPackages = 5000

// AssetPackages lists every package the signed-in account holds.
func (c *Client) AssetPackages(ctx context.Context) ([]AssetPackage, error) {
	if !c.Authenticated() {
		return nil, fmt.Errorf("aub: listing an account's packages needs a signed-in account")
	}
	var all []AssetPackage
	for offset := 0; offset < maxAssetPackages; {
		query := url.Values{}
		query.Set("limit", strconv.Itoa(assetPackagePage))
		query.Set("offset", strconv.Itoa(offset))
		var page struct {
			Packages []AssetPackage `json:"packages"`
			Total    int            `json:"total"`
		}
		if err := c.do(ctx, http.MethodGet, "/api/asset-packages", query, nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Packages...)
		offset += len(page.Packages)
		if len(page.Packages) == 0 || offset >= page.Total {
			break
		}
	}
	return all, nil
}

// AssetPackageArchive opens one package's original archive for reading. The
// caller closes it, and verifies what it reads: the length returned here is
// what the server DECLARED (-1 when it declared none).
func (c *Client) AssetPackageArchive(ctx context.Context, id string) (io.ReadCloser, int64, error) {
	if strings.TrimSpace(id) == "" {
		return nil, 0, fmt.Errorf("aub: a package archive needs a package id")
	}
	if !c.Authenticated() {
		return nil, 0, fmt.Errorf("aub: downloading a package needs a signed-in account")
	}
	endpoint := *c.baseURL
	path := "/api/asset-packages/" + url.PathEscape(id) + "/archive"
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, 0, fmt.Errorf("aub: building the package request: %w", err)
	}
	request.Header.Set("Authorization", c.Token())
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("aub: downloading the package: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		return nil, 0, newAPIError(path, response)
	}
	return response.Body, response.ContentLength, nil
}
