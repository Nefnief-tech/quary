package runners

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"quarry.dev/quarry/internal/ssrf"
)

const (
	paperAPIBase = "https://api.papermc.io/v2"
	paperProject = "paper"
)

// Paper runs the PaperMC server (api.papermc.io v2).
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

type paperProjectMeta struct {
	Versions []string `json:"versions"`
}

func (p *Paper) ListVersions(ctx context.Context) ([]CatalogVersion, error) {
	var m paperProjectMeta
	if err := fetchJSON(ctx, p.client, p.apiBase+"/projects/"+paperProject, &m); err != nil {
		return nil, fmt.Errorf("paper versions: %w", err)
	}
	out := make([]CatalogVersion, 0, len(m.Versions))
	for _, v := range m.Versions {
		out = append(out, CatalogVersion{MCVersion: v, Stable: true})
	}
	sort.Slice(out, func(i, j int) bool {
		return versionLess(out[j].MCVersion, out[i].MCVersion)
	})
	return out, nil
}

type paperBuilds struct {
	Builds []struct {
		Build     int    `json:"build"`
		Channel   string `json:"channel"`
		Downloads struct {
			Application struct {
				Name   string `json:"name"`
				SHA256 string `json:"sha256"`
			} `json:"application"`
		} `json:"downloads"`
	} `json:"builds"`
}

func (p *Paper) Resolve(ctx context.Context, mcVersion string) (Artifact, error) {
	var b paperBuilds
	url := fmt.Sprintf("%s/projects/%s/versions/%s/builds", p.apiBase, paperProject, mcVersion)
	if err := fetchJSON(ctx, p.client, url, &b); err != nil {
		return Artifact{}, fmt.Errorf("paper builds for %s: %w", mcVersion, err)
	}
	// Prefer the newest default-channel build; fall back to the newest build.
	best := -1
	fallback := -1
	for i, build := range b.Builds {
		if best == -1 || build.Build > b.Builds[best].Build {
			fallback = i
		}
		if build.Channel == "default" && (best == -1 || build.Build > b.Builds[best].Build) {
			best = i
		}
	}
	idx := best
	if idx == -1 {
		idx = fallback
	}
	if idx == -1 {
		return Artifact{}, fmt.Errorf("paper %s: no builds published", mcVersion)
	}
	build := b.Builds[idx]
	return Artifact{
		URL: fmt.Sprintf("%s/projects/%s/versions/%s/builds/%d/downloads/%s",
			p.apiBase, paperProject, mcVersion, build.Build, build.Downloads.Application.Name),
		SHA256: build.Downloads.Application.SHA256,
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
