package ssrf

import (
	"context"
	"errors"
	"net"
	"testing"
)

// checkWith runs check against a canned resolver answering with ips.
func checkWith(url string, ips ...net.IP) error {
	return check(context.Background(), &policy{
		lookupIP: func(ctx context.Context, host string) ([]net.IPAddr, error) {
			out := make([]net.IPAddr, 0, len(ips))
			for _, ip := range ips {
				out = append(out, net.IPAddr{IP: ip})
			}
			return out, nil
		},
	}, url)
}

func TestCheckAllowsPublicAddresses(t *testing.T) {
	cases := [][]net.IP{
		{net.ParseIP("8.8.8.8")},
		{net.ParseIP("1.1.1.1"), net.ParseIP("8.8.4.4")},
		{net.ParseIP("2606:4700:4700::1111")},
	}
	for i, ips := range cases {
		if err := checkWith("https://example.com/download.zip", ips...); err != nil {
			t.Fatalf("case %d: check with public IPs: %v", i, err)
		}
	}
}

func TestCheckRejectsReservedAddresses(t *testing.T) {
	cases := map[string]net.IP{
		"loopback":         net.ParseIP("127.0.0.1"),
		"ipv6 loopback":    net.ParseIP("::1"),
		"private 10/8":     net.ParseIP("10.1.2.3"),
		"private 172.16":   net.ParseIP("172.16.0.1"),
		"private 192.168":  net.ParseIP("192.168.1.1"),
		"ula fc00::/7":     net.ParseIP("fc00::1"),
		"link-local":       net.ParseIP("169.254.1.1"),
		"cgnat 100.64/10":  net.ParseIP("100.100.1.1"),
		"multicast":        net.ParseIP("224.0.0.1"),
		"unspecified":      net.ParseIP("0.0.0.0"),
		"mapped loopback":  net.ParseIP("::ffff:127.0.0.1"),
		"docker bridge":    net.ParseIP("172.17.0.2"),
		"metadata service": net.ParseIP("169.254.169.254"),
	}
	for name, ip := range cases {
		if err := checkWith("https://evil.example/file", ip); !errors.Is(err, ErrReserved) {
			t.Fatalf("%s: err = %v, want ErrReserved", name, err)
		}
	}
}

func TestCheckFailsClosedOnMixedResults(t *testing.T) {
	// One public, one private address: the whole host must be rejected.
	err := checkWith("https://rebind.example/file",
		net.ParseIP("8.8.8.8"), net.ParseIP("10.0.0.1"))
	if !errors.Is(err, ErrReserved) {
		t.Fatalf("mixed addresses: err = %v, want ErrReserved", err)
	}
}

func TestCheckSchemeAndHost(t *testing.T) {
	if err := checkWith("file:///etc/passwd"); !errors.Is(err, ErrScheme) {
		t.Fatalf("file scheme: err = %v, want ErrScheme", err)
	}
	if err := checkWith("gopher://example.com"); !errors.Is(err, ErrScheme) {
		t.Fatalf("gopher scheme: err = %v, want ErrScheme", err)
	}
	if err := checkWith("https:///no-host"); !errors.Is(err, ErrHost) {
		t.Fatalf("no host: err = %v, want ErrHost", err)
	}
}

func TestCheckLiteralIPs(t *testing.T) {
	if err := check(context.Background(), &policy{}, "https://127.0.0.1/x"); !errors.Is(err, ErrReserved) {
		t.Fatalf("literal loopback: err = %v, want ErrReserved", err)
	}
	// A public literal IP never touches the resolver, so this passes even
	// though the canned lookup is not consulted.
	if err := check(context.Background(), &policy{}, "https://93.184.216.34/x"); err != nil {
		t.Fatalf("literal public IP: %v", err)
	}
}

func TestCheckRejectsLiteralReservedIPBeforeLookup(t *testing.T) {
	if err := check(context.Background(), &policy{}, "https://169.254.169.254/latest/meta-data"); !errors.Is(err, ErrReserved) {
		t.Fatalf("literal metadata IP: err = %v, want ErrReserved", err)
	}
}

func TestReservedCGNATBoundaries(t *testing.T) {
	if !reserved(net.ParseIP("100.64.0.1")) || !reserved(net.ParseIP("100.127.255.254")) {
		t.Fatal("CGNAT range must be reserved")
	}
	if reserved(net.ParseIP("100.63.255.255")) || reserved(net.ParseIP("100.128.0.0")) {
		t.Fatal("addresses outside CGNAT range must not be reserved")
	}
}

func TestCheckLookupFailure(t *testing.T) {
	err := check(context.Background(), &policy{
		lookupIP: func(ctx context.Context, host string) ([]net.IPAddr, error) {
			return nil, errors.New("dns is down")
		},
	}, "https://example.com")
	if !errors.Is(err, ErrLookup) {
		t.Fatalf("lookup failure: err = %v, want ErrLookup", err)
	}
}
