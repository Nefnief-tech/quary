// Package ssrf guards every server-side fetch of a user-influenced URL.
// Outbound fetches (marketplace providers, direct URL installs, webhooks,
// runner catalogs) must go through a Guard; direct http.Get on such URLs is
// not allowed. The policy: http/https only, and the host must resolve
// exclusively to public, non-reserved IP addresses. An explicit allowlist of
// CIDRs exists for deployments with local artifact mirrors — it must never
// be widened to include loopback in production.
package ssrf

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"strings"
	"time"
)

var (
	ErrScheme   = errors.New("only http and https URLs are allowed")
	ErrHost     = errors.New("URL has no host")
	ErrReserved = errors.New("URL host resolves to a reserved or private address")
	ErrLookup   = errors.New("URL host could not be resolved")
)

// Guard validates URLs and dials on behalf of a component.
type Guard struct {
	allow    []*net.IPNet
	lookupIP func(ctx context.Context, host string) ([]net.IPAddr, error) // nil = system resolver
}

// NewGuard builds a Guard with an optional allowlist of CIDRs that are
// exempt from the reserved-address check (e.g. an internal artifact mirror).
func NewGuard(allowCIDRs []string) (*Guard, error) {
	g := &Guard{}
	for _, cidr := range allowCIDRs {
		_, parsed, err := net.ParseCIDR(strings.TrimSpace(cidr))
		if err != nil {
			return nil, fmt.Errorf("parse allow CIDR %q: %w", cidr, err)
		}
		g.allow = append(g.allow, parsed)
	}
	return g, nil
}

// DefaultGuard has no allowlist — everything reserved is rejected.
func DefaultGuard() *Guard { return &Guard{} }

func (g *Guard) lookup(ctx context.Context, host string) ([]net.IPAddr, error) {
	if g != nil && g.lookupIP != nil {
		return g.lookupIP(ctx, host)
	}
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

// Check validates rawURL against the policy without dialing.
func (g *Guard) Check(ctx context.Context, rawURL string) error {
	u, err := neturl.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("parse URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: %q", ErrScheme, u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return ErrHost
	}

	// Literal IPs need no lookup but get the same address checks.
	if ip := net.ParseIP(host); ip != nil {
		if g.reserved(ip) {
			return fmt.Errorf("%w: %s is a reserved address", ErrReserved, ip)
		}
		return nil
	}

	addrs, err := g.lookup(ctx, host)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrLookup, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("%w: no addresses for %q", ErrLookup, host)
	}
	for _, addr := range addrs {
		if g.reserved(addr.IP) {
			return fmt.Errorf("%w: %s", ErrReserved, addr.IP)
		}
	}
	return nil
}

// reserved reports whether ip must never be dialed from this component.
func (g *Guard) reserved(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range g.allow {
		if n.Contains(ip) {
			return false
		}
	}
	return reservedAddress(ip)
}

// reservedAddress classifies an IP against the static policy.
func reservedAddress(ip net.IP) bool {
	return ip.IsUnspecified() ||
		ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		cgnat(ip)
}

// cgnat matches 100.64.0.0/10 (carrier-grade NAT), which Go does not
// classify as private.
func cgnat(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	return v4[0] == 100 && v4[1] >= 64 && v4[1] < 128
}

// HTTPClient returns an *http.Client whose dialer re-validates every
// connection (guarding against DNS rebinding between Check and dial) and
// whose redirect handling re-validates every hop. Timeouts suit artifact
// downloads.
func (g *Guard) HTTPClient() *http.Client {
	return &http.Client{
		Timeout: 5 * time.Minute,
		Transport: &http.Transport{
			DialContext:           g.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			MaxIdleConnsPerHost:   4,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("stopped after 5 redirects")
			}
			return g.Check(req.Context(), req.URL.String())
		},
	}
}

// DialContext is a net.Dialer replacement that enforces the policy at dial
// time.
func (g *Guard) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("split address: %w", err)
	}

	if ip := net.ParseIP(host); ip != nil {
		if g.reserved(ip) {
			return nil, fmt.Errorf("%w: %s", ErrReserved, ip)
		}
	} else if err := g.Check(ctx, "https://"+host); err != nil {
		return nil, err
	}

	var d net.Dialer
	return d.DialContext(ctx, network, net.JoinHostPort(host, port))
}

// Package-level helpers for the default (no-allowlist) policy.

func Check(ctx context.Context, rawURL string) error {
	return DefaultGuard().Check(ctx, rawURL)
}

func NewHTTPClient() *http.Client {
	return DefaultGuard().HTTPClient()
}

func DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return DefaultGuard().DialContext(ctx, network, addr)
}

// DownloadFile fetches url through the guard into dest, verifies the
// sha256 hex checksum, and lands the file atomically. Used for
// version-pinned auxiliary binaries (e.g. tunnel agents); downloading
// without a pinned checksum is refused.
func (g *Guard) DownloadFile(ctx context.Context, url, dest, wantSHA256 string) error {
	if wantSHA256 == "" {
		return errors.New("refusing to download without a pinned checksum")
	}
	if err := g.Check(ctx, url); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := g.HTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}

	tmp := dest + ".download"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, err = io.Copy(io.MultiWriter(out, hash), resp.Body)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != strings.ToLower(wantSHA256) {
		_ = os.Remove(tmp)
		return fmt.Errorf("checksum mismatch (got %s, want %s)", got, wantSHA256)
	}
	return os.Rename(tmp, dest)
}
