package provider

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type Client struct{ options ClientOptions }

func NewClient(o ClientOptions) *Client {
	if o.Resolver == nil {
		o.Resolver = net.DefaultResolver
	}
	defaults := DefaultTimeoutConfig()
	o.ConnectTimeout = safeTimeout(o.ConnectTimeout, defaults.ConnectTimeout, MaxConnectTimeout)
	o.HeaderTimeout = safeTimeout(o.HeaderTimeout, defaults.HeaderTimeout, MaxHeaderTimeout)
	o.FirstEventTimeout = safeTimeout(o.FirstEventTimeout, defaults.FirstEventTimeout, MaxFirstEventTimeout)
	o.IdleTimeout = safeTimeout(o.IdleTimeout, defaults.IdleTimeout, MaxIdleTimeout)
	o.ProgressTimeout = safeTimeout(o.ProgressTimeout, defaults.ProgressTimeout, MaxProgressTimeout)
	return &Client{options: o}
}

func safeTimeout(value, fallback, maximum time.Duration) time.Duration {
	if value < MinProviderTimeout || value > maximum {
		return fallback
	}
	return value
}

type wireConn struct {
	net.Conn
	written *atomic.Bool
}

// ValidateEndpointConfig validates URL, protocol, port and CIDR configuration
// without resolving a hostname or opening a network connection. Every actual
// dial still revalidates the resolved addresses against the same network policy.
func ValidateEndpointConfig(endpoint Endpoint) error {
	_, _, err := validateEndpoint(endpoint)
	return err
}

func (c *wireConn) Write(p []byte) (int, error) {
	n, e := c.Conn.Write(p)
	if n > 0 {
		c.written.Store(true)
	}
	return n, e
}

func validateEndpoint(endpoint Endpoint) (*url.URL, []netip.Prefix, error) {
	u, e := url.Parse(endpoint.URL)
	if e != nil || u.Host == "" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && endpoint.Network.AllowHTTP)) {
		return nil, nil, failure("invalid_endpoint", 503, ClassConfiguration, ConsumptionNotSent)
	}
	h := u.Hostname()
	if h == "" || strings.ContainsAny(h, "%\\\r\n\t ") {
		return nil, nil, failure("invalid_endpoint", 503, ClassConfiguration, ConsumptionNotSent)
	}
	for _, r := range h {
		if r > 127 {
			return nil, nil, failure("invalid_endpoint", 503, ClassConfiguration, ConsumptionNotSent)
		}
	}
	port := u.Port()
	if port == "" {
		port = "443"
		if u.Scheme == "http" {
			port = "80"
		}
	}
	n, e := strconv.ParseUint(port, 10, 16)
	if e != nil || n == 0 {
		return nil, nil, failure("invalid_endpoint", 503, ClassConfiguration, ConsumptionNotSent)
	}
	allowed := false
	if len(endpoint.Network.AllowedPorts) == 0 {
		allowed = (u.Scheme == "https" && n == 443) || (u.Scheme == "http" && n == 80)
	}
	for _, p := range endpoint.Network.AllowedPorts {
		if uint64(p) == n {
			allowed = true
		}
	}
	if !allowed {
		return nil, nil, failure("endpoint_port_forbidden", 503, ClassConfiguration, ConsumptionNotSent)
	}
	var prefixes []netip.Prefix
	for _, s := range endpoint.Network.AllowedCIDRs {
		p, e := netip.ParsePrefix(s)
		if e != nil {
			return nil, nil, failure("invalid_network_policy", 503, ClassConfiguration, ConsumptionNotSent)
		}
		prefixes = append(prefixes, p.Masked())
	}
	return u, prefixes, nil
}

func permittedIP(ip netip.Addr, allow []netip.Prefix) bool {
	ip = ip.Unmap()
	// Explicit ranges may allow private/loopback endpoints but never metadata,
	// link-local, unspecified or multicast destinations.
	if !ip.IsValid() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	for _, s := range []string{"169.254.0.0/16", "100.100.100.200/32", "fd00:ec2::254/128"} {
		if netip.MustParsePrefix(s).Contains(ip) {
			return false
		}
	}
	for _, p := range allow {
		if p.Contains(ip) {
			return true
		}
	}
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() {
		return false
	}
	for _, s := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32"} {
		if netip.MustParsePrefix(s).Contains(ip) {
			return false
		}
	}
	return true
}

func (c *Client) transport(endpoint Endpoint, u *url.URL, allow []netip.Prefix, written *atomic.Bool) *http.Transport {
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, c.options.ConnectTimeout)
		defer cancel()
		host, port, e := net.SplitHostPort(address)
		if e != nil || !strings.EqualFold(host, u.Hostname()) {
			return nil, failure("endpoint_dial_forbidden", 503, ClassConfiguration, ConsumptionNotSent)
		}
		var ips []net.IPAddr
		if ip, e := netip.ParseAddr(host); e == nil {
			ips = []net.IPAddr{{IP: net.IP(ip.AsSlice())}}
		} else {
			ips, e = c.options.Resolver.LookupIPAddr(ctx, host)
			if e != nil {
				return nil, failure("dns_unavailable", 503, ClassNetwork, ConsumptionNotSent)
			}
		}
		if len(ips) == 0 {
			return nil, failure("dns_unavailable", 503, ClassNetwork, ConsumptionNotSent)
		}
		for _, ip := range ips {
			a, ok := netip.AddrFromSlice(ip.IP)
			if !ok || ip.Zone != "" || !permittedIP(a, allow) {
				return nil, failure("endpoint_ip_forbidden", 503, ClassConfiguration, ConsumptionNotSent)
			}
		}
		// Do not hand the hostname to the dialer: that would resolve again and
		// reintroduce a DNS rebinding gap after validation.
		d := net.Dialer{Timeout: c.options.ConnectTimeout}
		var last error
		for _, ip := range ips {
			conn, e := d.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if e == nil {
				return conn, nil
			}
			last = e
			if ctx.Err() != nil {
				break
			}
		}
		return nil, last
	}
	t := &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: false, ResponseHeaderTimeout: c.options.HeaderTimeout, MaxResponseHeaderBytes: 32 << 10, ForceAttemptHTTP2: false, TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}}
	t.DialContext = func(ctx context.Context, n, a string) (net.Conn, error) {
		conn, e := dial(ctx, n, a)
		if e != nil {
			return nil, e
		}
		return &wireConn{Conn: conn, written: written}, nil
	}
	t.DialTLSContext = func(ctx context.Context, n, a string) (net.Conn, error) {
		conn, e := dial(ctx, n, a)
		if e != nil {
			return nil, e
		}
		tlsConn := tls.Client(conn, &tls.Config{RootCAs: c.options.RootCAs, ServerName: u.Hostname(), MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}})
		handshakeCtx, cancel := context.WithTimeout(ctx, c.options.ConnectTimeout)
		defer cancel()
		if e = tlsConn.HandshakeContext(handshakeCtx); e != nil {
			_ = conn.Close()
			return nil, failure("upstream_tls_failed", 502, ClassNetwork, ConsumptionNotSent)
		}
		return &wireConn{Conn: tlsConn, written: written}, nil
	}
	return t
}
