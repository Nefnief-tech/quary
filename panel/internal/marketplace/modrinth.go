// Package marketplace implements marketplace providers. Modrinth is the
// first; the provider interface matches the module system's shape so later
// providers (Hangar, CurseForge, custom registry) slot in.
package marketplace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"quarry.dev/quarry/internal/ssrf"
)

const modrinthAPIBase = "https://api.modrinth.com/v2"

// user-agent per Modrinth's API guidelines: identify the tool.
const userAgent = "quarry-panel/0.1 (github.com/quarry-sh/quarry)"

// SearchResult is one plugin/project row.
type SearchResult struct {
	ProjectID  string `json:"project_id"`
	Slug       string `json:"slug"`
	Title      string `json:"title"`
	Author     string `json:"author"`
	Desc       string `json:"description"`
	Downloads  int64  `json:"downloads"`
	IconURL    string `json:"icon_url"`
	ServerSide string `json:"server_side"`
}

// ResolvedDownload points at a checksummed jar file.
type ResolvedDownload struct {
	Version  string `json:"version"`
	Filename string `json:"filename"`
	URL      string `json:"url"`
	SHA1     string `json:"sha1"`
	SHA512   string `json:"sha512"`
}

type modrinthVersion struct {
	VersionNumber string         `json:"version_number"`
	Files         []modrinthFile `json:"files"`
}

type modrinthFile struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
	Primary  bool   `json:"primary"`
	Hashes   struct {
		SHA1   string `json:"sha1"`
		SHA512 string `json:"sha512"`
	} `json:"hashes"`
}

// Modrinth is the Modrinth marketplace provider.
type Modrinth struct {
	apiBase string
	client  *http.Client
}

func NewModrinth() *Modrinth {
	return &Modrinth{apiBase: modrinthAPIBase, client: ssrf.NewHTTPClient()}
}

func NewModrinthWithBase(apiBase string, client *http.Client) *Modrinth {
	return &Modrinth{apiBase: apiBase, client: client}
}

func (m *Modrinth) ID() string { return "modrinth" }

// Search finds plugins compatible with the MC version and loader.
// loader is the runner's plugin loader name ("paper", "fabric", …).
func (m *Modrinth) Search(ctx context.Context, query, mcVersion, loader string, limit, offset int) ([]SearchResult, int64, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	facets := fmt.Sprintf(
		`[["project_type:plugin"],[%s],[%s]]`,
		jsonString("versions:"+mcVersion), jsonString("categories:"+loader),
	)
	q := url.Values{}
	q.Set("query", query)
	q.Set("facets", facets)
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(offset))

	var res struct {
		Hits      []SearchResult `json:"hits"`
		TotalHits int64          `json:"total_hits"`
	}
	if err := m.getJSON(ctx, "/search?"+q.Encode(), &res); err != nil {
		return nil, 0, fmt.Errorf("modrinth search: %w", err)
	}
	return res.Hits, res.TotalHits, nil
}

// Resolve returns the newest plugin file compatible with the MC version and
// loader for a project.
func (m *Modrinth) Resolve(ctx context.Context, projectID, mcVersion, loader string) (ResolvedDownload, error) {
	q := url.Values{}
	q.Set("game_versions", "["+jsonString(mcVersion)+"]")
	q.Set("loaders", "["+jsonString(loader)+"]")
	var versions []modrinthVersion
	if err := m.getJSON(ctx, "/project/"+url.PathEscape(projectID)+"/version?"+q.Encode(), &versions); err != nil {
		return ResolvedDownload{}, fmt.Errorf("modrinth versions: %w", err)
	}
	if len(versions) == 0 {
		return ResolvedDownload{}, fmt.Errorf("no %s plugin version for Minecraft %s", loader, mcVersion)
	}
	v := versions[0]
	if len(v.Files) == 0 {
		return ResolvedDownload{}, fmt.Errorf("modrinth version %s has no files", v.VersionNumber)
	}
	file := v.Files[0]
	for _, f := range v.Files {
		if f.Primary {
			file = f
			break
		}
	}
	return ResolvedDownload{
		Version:  v.VersionNumber,
		Filename: file.Filename,
		URL:      file.URL,
		SHA1:     file.Hashes.SHA1,
		SHA512:   file.Hashes.SHA512,
	}, nil
}

func (m *Modrinth) getJSON(ctx context.Context, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.apiBase+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

// jsonString escapes a value for embedding in Modrinth's JSON-in-query-param
// facets.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
