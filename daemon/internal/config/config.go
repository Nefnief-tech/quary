// Package config loads daemon configuration. The daemon is configured
// entirely from the environment (QUARRY_*), so it runs unmodified in a
// container or under systemd.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// PanelAddr is host:port of the panel's node gRPC listener.
	PanelAddr string
	// NodeName must match the name the admin created the node with.
	NodeName string
	// EnrollmentToken is needed until the daemon has an identity on disk.
	EnrollmentToken string
	// DataDir holds the daemon identity (keys) — keep it persistent.
	DataDir string
	// ServersRoot holds per-server data directories.
	ServersRoot string
	// ArtifactAllowCIDRs exempts internal artifact mirrors from the SSRF
	// policy (comma-separated; empty = public addresses only).
	ArtifactAllowCIDRs []string
	// CAFile is the path to the panel CA certificate (PEM).
	CAFile string
	// ServerName optionally overrides the TLS server name for verification
	// (when connecting via IP or a name not on the panel cert).
	ServerName string
	// HeartbeatInterval is how often a beat is sent.
	HeartbeatInterval time.Duration
	// ContainerUID is the UID/GID server containers run as. It must match
	// the daemon's own effective UID so the bind-mounted server directories
	// are writable inside and out. Default 1500 (the packaged daemon user).
	ContainerUID int
}

func Load() (Config, error) {
	c := Config{
		PanelAddr:         os.Getenv("QUARRY_PANEL_ADDR"),
		NodeName:          os.Getenv("QUARRY_NODE_NAME"),
		EnrollmentToken:   os.Getenv("QUARRY_ENROLLMENT_TOKEN"),
		DataDir:           envOr("QUARRY_DATA_DIR", "./data"),
		ServersRoot:       envOr("QUARRY_SERVERS_DIR", "./data/servers"),
		CAFile:            os.Getenv("QUARRY_CA_FILE"),
		ServerName:        os.Getenv("QUARRY_TLS_SERVER_NAME"),
		HeartbeatInterval: 10 * time.Second,
		ContainerUID:      1500,
	}
	if v := os.Getenv("QUARRY_CONTAINER_UID"); v != "" {
		uid, err := strconv.Atoi(v)
		if err != nil || uid <= 0 {
			return c, fmt.Errorf("QUARRY_CONTAINER_UID must be a positive integer")
		}
		c.ContainerUID = uid
	}
	if raw := os.Getenv("QUARRY_ARTIFACT_ALLOW_CIDRS"); raw != "" {
		for _, cidr := range strings.Split(raw, ",") {
			if cidr = strings.TrimSpace(cidr); cidr != "" {
				c.ArtifactAllowCIDRs = append(c.ArtifactAllowCIDRs, cidr)
			}
		}
	}
	if c.PanelAddr == "" {
		return c, errors.New("QUARRY_PANEL_ADDR is required")
	}
	if c.NodeName == "" {
		return c, errors.New("QUARRY_NODE_NAME is required")
	}
	if c.CAFile == "" {
		return c, errors.New("QUARRY_CA_FILE is required (path to the panel CA certificate)")
	}
	if v := os.Getenv("QUARRY_HEARTBEAT_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return c, fmt.Errorf("QUARRY_HEARTBEAT_INTERVAL: %w", err)
		}
		c.HeartbeatInterval = d
	}
	return c, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
