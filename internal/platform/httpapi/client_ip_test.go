package httpapi

import (
	"net"
	"net/http"
	"testing"
)

func mustCIDR(t *testing.T, cidr string) *net.IPNet {
	t.Helper()
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatal(err)
	}
	return network
}

// TestClientIPTrustsForwardedHeaderOnlyFromTrustedProxies pins the fix for a
// report finding: clientIP used to accept X-Forwarded-For from any
// connection, so a client that could reach the API directly (bypassing the
// SvelteKit proxy that normally sets this header) could forge it to pick its
// own login rate-limit bucket or forge the IP recorded in audit trails. Now
// the header is only honored when RemoteAddr falls inside a configured
// trusted-proxy network; everything else falls back to RemoteAddr.
func TestClientIPTrustsForwardedHeaderOnlyFromTrustedProxies(t *testing.T) {
	t.Cleanup(func() { SetTrustedProxyNets(nil) })
	SetTrustedProxyNets([]*net.IPNet{mustCIDR(t, "172.16.0.0/12")})

	trusted := &http.Request{RemoteAddr: "172.20.0.5:54321", Header: http.Header{}}
	trusted.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := clientIP(trusted); got != "203.0.113.9" {
		t.Fatalf("trusted proxy: clientIP = %q, want the forwarded address", got)
	}

	untrusted := &http.Request{RemoteAddr: "198.51.100.7:54321", Header: http.Header{}}
	untrusted.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := clientIP(untrusted); got != "198.51.100.7" {
		t.Fatalf("untrusted connection: clientIP = %q, want RemoteAddr, not the forged header", got)
	}

	// No trusted proxies configured at all: every connection falls back to
	// RemoteAddr, even one that would otherwise match a network.
	SetTrustedProxyNets(nil)
	noConfig := &http.Request{RemoteAddr: "172.20.0.5:54321", Header: http.Header{}}
	noConfig.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := clientIP(noConfig); got != "172.20.0.5" {
		t.Fatalf("no trusted proxies configured: clientIP = %q, want RemoteAddr", got)
	}
}
