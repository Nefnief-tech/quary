// Package runners implements the Runner abstraction: declarative support for
// Minecraft server software. Each runner knows how to list versions from its
// upstream API, resolve a checksummed artifact for a version, pick the Java
// runtime, and describe the launch command.
package runners

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// PluginKind describes what a server loads into its content directories.
type PluginKind string

const (
	Plugins PluginKind = "plugins"
	Mods    PluginKind = "mods"
	None    PluginKind = "none"
)

// Artifact points at a checksummed server jar.
type Artifact struct {
	URL    string
	SHA256 string
	SHA1   string
	Name   string // target filename inside the server directory
}

// Runner is the module boundary for server software support. First-party
// runners register here; third-party ones (Phase 4) plug in through the
// module system.
type Runner interface {
	ID() string
	// ListVersions returns the available versions from the upstream API.
	ListVersions(ctx context.Context) ([]CatalogVersion, error)
	// Resolve returns the artifact for a Minecraft version (latest build).
	Resolve(ctx context.Context, mcVersion string) (Artifact, error)
	// JavaVersion maps a Minecraft version to the required major Java.
	JavaVersion(mcVersion string) int
	// Launch is the container argv. Memory flags are included.
	Launch(memoryMB int) []string
	// PluginKind reports the content directory this runner uses.
	PluginKind() PluginKind
}

// CatalogVersion is a version entry as listed by a runner.
type CatalogVersion struct {
	MCVersion string    `json:"mc_version"`
	Stable    bool      `json:"stable"`
	Published time.Time `json:"published,omitempty"`
}

// ImageForJava is the lean base image per Java major. One image per Java
// version; the daemon picks it from the runner's JavaVersion mapping.
func ImageForJava(javaVersion int) string {
	return fmt.Sprintf("eclipse-temurin:%d-jre-noble", javaVersion)
}

var mcVersionRe = regexp.MustCompile(`^(\d+)\.(\d+)(?:\.(\d+))?$`)

// mcVersionKey parses "1.21.1" into comparable numbers (0 for missing parts).
func mcVersionKey(mcVersion string) (major, minor, patch int, err error) {
	m := mcVersionRe.FindStringSubmatch(strings.TrimSpace(mcVersion))
	if m == nil {
		return 0, 0, 0, fmt.Errorf("invalid minecraft version %q", mcVersion)
	}
	nums := make([]int, 3)
	for i := 0; i < 3; i++ {
		if m[i+1] != "" {
			n, err := strconv.Atoi(m[i+1])
			if err != nil {
				return 0, 0, 0, fmt.Errorf("invalid minecraft version %q", mcVersion)
			}
			nums[i] = n
		}
	}
	return nums[0], nums[1], nums[2], nil
}

// atLeast reports whether mcVersion >= want ("1.18" style, patch optional).
func atLeast(mcVersion, want string) bool {
	ma, mi, pa, err := mcVersionKey(mcVersion)
	if err != nil {
		return false
	}
	wa, wi, wp, err := mcVersionKey(want)
	if err != nil {
		return false
	}
	if ma != wa {
		return ma > wa
	}
	if mi != wi {
		return mi > wi
	}
	return pa >= wp
}

// JavaForMC is the common Java mapping shared by vanilla-like runners.
// Java 25 for the 26.x releases, 21 for 1.20.5+, 17 for 1.17–1.20.4, 8 below.
func JavaForMC(mcVersion string) int {
	switch {
	case atLeast(mcVersion, "26"):
		return 25
	case atLeast(mcVersion, "1.20.5"):
		return 21
	case atLeast(mcVersion, "1.17"):
		return 17
	default:
		return 8
	}
}
