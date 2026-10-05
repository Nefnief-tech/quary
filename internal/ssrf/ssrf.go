// Package ssrf guards every server-side fetch of a user-influenced URL.
// Outbound fetches (marketplace providers, direct URL installs, webhooks)
// must go through Check/NewHTTPClient; direct http.Get on such URLs is not
// allowed. The policy: http/https only, and the host must resolve
// exclusively to public, non-reserved IP addresses.
package ssrf

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	neturl "net/url"
	"strings"
	"time"
)

var (
	ErrScheme   = errors.New("only http and https URLs are allowed")
	ErrHost     = errors.New("URL has no host")
	ErrReserved = errors.New("URL host resolves to a reserved or private address")
	ErrLookup   = errors.New("URL host could not be resolved")
)

// policy is injectable for tests; a nil lookup func uses the system resolver.
type policy struct {
	lookupIP func(ctx context.Context, host string) ([]net.IPAddr, error)
}

func (p *policy) lookup(ctx context.Context, host string) ([]net.IPAddr, error) {
	if p != nil && p.lookupIP != nil {
		return p.lookupIP(ctx, host)
	}
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

// Check validates rawURL against the policy without dialing.
func Check(ctx context.Context, rawURL string) error {
	return check(ctx, &policy{}, rawURL)
}

func check(ctx context.Context, p *policy, rawURL string) error {
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
		if reserved(ip) {
			return fmt.Errorf("%w: %s is a reserved address", ErrReserved, ip)
		}
		return nil
	}

	addrs, err := p.lookup(ctx, host)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrLookup, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("%w: no addresses for %q", ErrLookup, host)
	}
	for _, addr := range addrs {
		if reserved(addr.IP) {
			return fmt.Errorf("%w: %s", ErrReserved, addr.IP)
		}
	}
	return nil
}

// reserved reports whether ip must never be dialed from the panel.
func reserved(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
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

// NewHTTPClient returns an *http.Client whose dialer re-validates every
// connection (guarding against DNS rebinding between Check and dial) and
// whose redirect handling re-validates every hop. Timeouts are chosen for
// artifact downloads.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 5 * time.Minute,
		Transport: &http.Transport{
			DialContext:           safeDialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			MaxIdleConnsPerHost:   4,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("stopped after 5 redirects")
			}
			return Check(req.Context(), req.URL.String())
		},
	}
}

func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("split address: %w", err)
	}

	p := &policy{}
	// Literal IPs: enforce the address policy before dialing.
	if ip := net.ParseIP(host); ip != nil {
		if reserved(ip) {
			return nil, fmt.Errorf("%w: %s", ErrReserved, ip)
		}
	} else if err := check(ctx, p, "https://"+host); err != nil {
		return nil, err
	}

	var d net.Dialer
	return d.DialContext(ctx, network, net.JoinHostPort(host, port))
}
