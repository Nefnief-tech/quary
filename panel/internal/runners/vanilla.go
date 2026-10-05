package runners

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"quarry.dev/quarry/internal/ssrf"
)

const vanillaManifestURL = "https://piston-meta.mojang.com/mc/game/version_manifest_v2.json"

// Vanilla runs the official Mojang server jar.
type Vanilla struct {
	manifestURL string
	client      *http.Client
}

func NewVanilla() *Vanilla {
	return &Vanilla{manifestURL: vanillaManifestURL, client: ssrf.NewHTTPClient()}
}

// NewVanillaWithBase points the runner at a different manifest (tests,
// mirrors). The client should be SSRF-guarded.
func NewVanillaWithBase(manifestURL string, client *http.Client) *Vanilla {
	return &Vanilla{manifestURL: manifestURL, client: client}
}

func (v *Vanilla) ID() string                   { return "vanilla" }
func (v *Vanilla) PluginKind() PluginKind       { return None }
func (v *Vanilla) JavaVersion(mc string) int    { return JavaForMC(mc) }
func (v *Vanilla) Launch(memoryMB int) []string { return javaLaunch(memoryMB) }

type vanillaManifest struct {
	Versions []struct {
		ID          string    `json:"id"`
		Type        string    `json:"type"`
		URL         string    `json:"url"`
		ReleaseTime time.Time `json:"releaseTime"`
	} `json:"versions"`
}

func (v *Vanilla) ListVersions(ctx context.Context) ([]CatalogVersion, error) {
	var m vanillaManifest
	if err := fetchJSON(ctx, v.client, v.manifestURL, &m); err != nil {
		return nil, fmt.Errorf("vanilla manifest: %w", err)
	}
	out := make([]CatalogVersion, 0, len(m.Versions))
	for _, e := range m.Versions {
		out = append(out, CatalogVersion{
			MCVersion: e.ID,
			Stable:    e.Type == "release",
			Published: e.ReleaseTime,
		})
	}
	return out, nil
}

type vanillaVersionMeta struct {
	Downloads struct {
		Server struct {
			URL  string `json:"url"`
			SHA1 string `json:"sha1"`
		} `json:"server"`
	} `json:"downloads"`
}

func (v *Vanilla) Resolve(ctx context.Context, mcVersion string) (Artifact, error) {
	var m vanillaManifest
	if err := fetchJSON(ctx, v.client, v.manifestURL, &m); err != nil {
		return Artifact{}, err
	}
	var metaURL string
	for _, e := range m.Versions {
		if e.ID == mcVersion {
			metaURL = e.URL
			break
		}
	}
	if metaURL == "" {
		return Artifact{}, fmt.Errorf("vanilla: version %q not found", mcVersion)
	}

	var meta vanillaVersionMeta
	if err := fetchJSON(ctx, v.client, metaURL, &meta); err != nil {
		return Artifact{}, fmt.Errorf("vanilla version meta: %w", err)
	}
	if meta.Downloads.Server.URL == "" {
		return Artifact{}, fmt.Errorf("vanilla %s: no server download", mcVersion)
	}
	return Artifact{
		URL:  meta.Downloads.Server.URL,
		SHA1: meta.Downloads.Server.SHA1,
		Name: "server.jar",
	}, nil
}

// javaLaunch is the common JVM argv (Aikar flags are a later-phase option).
func javaLaunch(memoryMB int) []string {
	return []string{
		"java",
		fmt.Sprintf("-Xms%dM", memoryMB),
		fmt.Sprintf("-Xmx%dM", memoryMB),
		"-jar", "server.jar", "nogui",
	}
}

// fetchJSON GETs a URL (through the guard client) and decodes JSON.
func fetchJSON(ctx context.Context, client *http.Client, url string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}
