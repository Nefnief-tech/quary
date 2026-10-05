package runners

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"

	"quarry.dev/quarry/internal/ssrf"
)

const fabricMetaBase = "https://meta.fabricmc.net/v2"

// Fabric runs the Fabric server launcher (meta.fabricmc.net).
type Fabric struct {
	metaBase string
	client   *http.Client
}

func NewFabric() *Fabric {
	return &Fabric{metaBase: fabricMetaBase, client: ssrf.NewHTTPClient()}
}

func NewFabricWithBase(metaBase string, client *http.Client) *Fabric {
	return &Fabric{metaBase: metaBase, client: client}
}

func (f *Fabric) ID() string                   { return "fabric" }
func (f *Fabric) PluginKind() PluginKind       { return Mods }
func (f *Fabric) JavaVersion(mc string) int    { return JavaForMC(mc) }
func (f *Fabric) Launch(memoryMB int) []string { return javaLaunch(memoryMB) }

type fabricGameVersion struct {
	Version string `json:"version"`
	Stable  bool   `json:"stable"`
}

func (f *Fabric) ListVersions(ctx context.Context) ([]CatalogVersion, error) {
	var m []fabricGameVersion
	if err := fetchJSON(ctx, f.client, f.metaBase+"/versions/game", &m); err != nil {
		return nil, fmt.Errorf("fabric game versions: %w", err)
	}
	out := make([]CatalogVersion, 0, len(m))
	for _, e := range m {
		out = append(out, CatalogVersion{MCVersion: e.Version, Stable: e.Stable})
	}
	return out, nil
}

type fabricLoaderEntry struct {
	Loader struct {
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
	} `json:"loader"`
}

type fabricInstallerEntry struct {
	Version string `json:"version"`
	Stable  bool   `json:"stable"`
}

// Resolve builds the universal server launcher URL for the latest stable
// loader and installer. The launcher jar is small, so the panel downloads it
// once to compute the sha256 the daemon will verify against.
func (f *Fabric) Resolve(ctx context.Context, mcVersion string) (Artifact, error) {
	var loaders []fabricLoaderEntry
	if err := fetchJSON(ctx, f.client, f.metaBase+"/versions/loader/"+mcVersion, &loaders); err != nil {
		return Artifact{}, fmt.Errorf("fabric loaders for %s: %w", mcVersion, err)
	}
	loader := ""
	for _, e := range loaders {
		if e.Loader.Stable {
			loader = e.Loader.Version
			break
		}
	}
	if loader == "" {
		return Artifact{}, fmt.Errorf("fabric %s: no stable loader", mcVersion)
	}

	var installers []fabricInstallerEntry
	if err := fetchJSON(ctx, f.client, f.metaBase+"/versions/installer", &installers); err != nil {
		return Artifact{}, fmt.Errorf("fabric installers: %w", err)
	}
	installer := ""
	for _, e := range installers {
		if e.Stable {
			installer = e.Version
			break
		}
	}
	if installer == "" {
		return Artifact{}, fmt.Errorf("fabric: no stable installer")
	}

	url := fmt.Sprintf("%s/versions/loader/%s/%s/%s/server/jar",
		f.metaBase, mcVersion, loader, installer)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Artifact{}, err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return Artifact{}, fmt.Errorf("fabric launcher download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return Artifact{}, fmt.Errorf("fabric launcher download: status %d", resp.StatusCode)
	}
	h := sha256.New()
	if _, err := io.Copy(h, resp.Body); err != nil {
		return Artifact{}, err
	}
	return Artifact{
		URL:    url,
		SHA256: hex.EncodeToString(h.Sum(nil)),
		Name:   "server.jar",
	}, nil
}
