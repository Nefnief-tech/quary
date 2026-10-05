package runners

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"quarry.dev/quarry/internal/ssrf"
)

// paperAPIBase is the PaperMC "Fill" v3 API (api.papermc.io v2 is gone).
const paperAPIBase = "https://fill.papermc.io/v3"

// Paper runs the PaperMC server.
type Paper struct {
	apiBase string
	client  *http.Client
}

func NewPaper() *Paper {
	return &Paper{apiBase: paperAPIBase, client: ssrf.NewHTTPClient()}
}

func NewPaperWithBase(apiBase string, client *http.Client) *Paper {
	return &Paper{apiBase: apiBase, client: client}
}

func (p *Paper) ID() string                   { return "paper" }
func (p *Paper) PluginKind() PluginKind       { return Plugins }
func (p *Paper) JavaVersion(mc string) int    { return JavaForMC(mc) }
func (p *Paper) Launch(memoryMB int) []string { return javaLaunch(memoryMB) }

type paperProject struct {
	Versions map[string][]string `json:"versions"`
}

func (p *Paper) ListVersions(ctx context.Context) ([]CatalogVersion, error) {
	var m paperProject
	if err := fetchJSON(ctx, p.client, p.apiBase+"/projects/paper", &m); err != nil {
		return nil, fmt.Errorf("paper versions: %w", err)
	}
	out := make([]CatalogVersion, 0)
	for _, versions := range m.Versions {
		for _, v := range versions {
			out = append(out, CatalogVersion{MCVersion: v, Stable: paperStable(v)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return versionLess(out[j].MCVersion, out[i].MCVersion)
	})
	return out, nil
}

// paperStable filters release candidates and pre-releases.
func paperStable(v string) bool {
	return !strings.Contains(v, "-rc") && !strings.Contains(v, "-pre") && !strings.Contains(v, "-snapshot")
}

type paperLatestBuild struct {
	ID        int    `json:"id"`
	Channel   string `json:"channel"`
	Downloads map[string]struct {
		Name      string `json:"name"`
		Checksums struct {
			SHA256 string `json:"sha256"`
		} `json:"checksums"`
		URL string `json:"url"`
	} `json:"downloads"`
}

// Resolve fetches the latest build for a version. The Fill API publishes a
// SHA-256 checksum for every download.
func (p *Paper) Resolve(ctx context.Context, mcVersion string) (Artifact, error) {
	var b paperLatestBuild
	url := fmt.Sprintf("%s/projects/paper/versions/%s/builds/latest", p.apiBase, mcVersion)
	if err := fetchJSON(ctx, p.client, url, &b); err != nil {
		return Artifact{}, fmt.Errorf("paper latest build for %s: %w", mcVersion, err)
	}
	dl, ok := b.Downloads["server:default"]
	if !ok {
		return Artifact{}, fmt.Errorf("paper %s build %d: no server download", mcVersion, b.ID)
	}
	return Artifact{
		URL:    dl.URL,
		SHA256: dl.Checksums.SHA256,
		Name:   "server.jar",
	}, nil
}

// versionLess compares MC version strings numerically ("1.2" < "1.10").
func versionLess(a, b string) bool {
	am := parseVersionParts(a)
	bm := parseVersionParts(b)
	for i := 0; i < 3; i++ {
		if am[i] != bm[i] {
			return am[i] < bm[i]
		}
	}
	return false
}

func parseVersionParts(v string) [3]int {
	var out [3]int
	n := 0
	start := 0
	for i := 0; i <= len(v) && n < 3; i++ {
		if i == len(v) || v[i] == '.' {
			if num, err := strconv.Atoi(v[start:i]); err == nil {
				out[n] = num
			}
			n++
			start = i + 1
		}
	}
	return out
}

var _ = ssrf.DefaultGuard
