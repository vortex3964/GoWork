package netfetch

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// lookupIPFunc resolves a hostname. Tests replace it to exercise the dial
// guard without touching real DNS.
type lookupIPFunc func(ctx context.Context, host string) ([]net.IP, error)

func defaultLookupIP(ctx context.Context, host string) ([]net.IP, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		ips = append(ips, addr.IP)
	}
	return ips, nil
}

// validateURL enforces the request policy that does not require DNS: only
// HTTP(S), no embedded userinfo, a host is present, and literal private
// addresses are refused immediately.
func validateURL(u *url.URL) error {
	if u == nil {
		return ErrInvalidURL
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("%w: %q", ErrUnsupportedScheme, u.Scheme)
	}
	if u.Host == "" || u.Hostname() == "" {
		return ErrInvalidURL
	}
	if u.User != nil {
		return ErrUserInfo
	}

	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("%w: localhost", ErrBlockedAddress)
	}
	if ip := parseLiteralIP(host); ip != nil && isBlockedIP(ip) {
		return fmt.Errorf("%w: %s", ErrBlockedAddress, host)
	}
	return nil
}

// dialContext resolves the destination once, rejects every non-public
// address, and then dials the validated IP directly. Dialing the checked IP
// (instead of the hostname) prevents a DNS rebinding answer between the
// check and the connection.
func (c *Client) dialContext(dialer *net.Dialer) func(ctx context.Context, network, address string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("netfetch: invalid dial address: %w", err)
		}
		ips, err := c.lookupHost(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("netfetch: resolve host: %w", err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("netfetch: host %s did not resolve", host)
		}
		for _, ip := range ips {
			if isBlockedIP(ip) {
				return nil, fmt.Errorf("%w: %s", ErrBlockedAddress, host)
			}
		}

		var lastErr error
		for _, ip := range ips {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("no usable address for %s", host)
		}
		return nil, fmt.Errorf("netfetch: dial host: %w", lastErr)
	}
}

func (c *Client) lookupHost(ctx context.Context, host string) ([]net.IP, error) {
	if ip := parseLiteralIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	return c.lookupIP(ctx, host)
}

func parseLiteralIP(host string) net.IP {
	if ip := net.ParseIP(host); ip != nil {
		return ip
	}
	// IPv6 zone identifiers (fe80::1%eth0) are not accepted by ParseIP.
	if i := strings.LastIndexByte(host, '%'); i > 0 {
		if ip := net.ParseIP(host[:i]); ip != nil {
			return ip
		}
	}
	return nil
}

// isBlockedIP reports whether an address is loopback, private, link-local,
// unspecified, multicast, or otherwise reserved. Both IPv4 and IPv6 forms
// are handled, including IPv4-mapped IPv6 addresses.
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsUnspecified() ||
		ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 0 { // 0.0.0.0/8
			return true
		}
		for _, block := range reservedIPv4 {
			if block.Contains(ip4) {
				return true
			}
		}
	}
	return false
}

var reservedIPv4 = mustCIDRs(
	"100.64.0.0/10", // carrier-grade NAT
	"192.0.0.0/24",  // IETF protocol assignments
	"198.18.0.0/15", // benchmarking
	"240.0.0.0/4",   // reserved, including broadcast
)

func mustCIDRs(cidrs ...string) []*net.IPNet {
	blocks := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, block, err := net.ParseCIDR(cidr)
		if err != nil {
			panic("netfetch: invalid reserved CIDR " + cidr + ": " + err.Error())
		}
		blocks = append(blocks, block)
	}
	return blocks
}

// checkRedirect enforces the redirect limit and reapplies the URL policy.
// Hostnames are resolved here as well, and dialContext validates the answers
// again immediately before connecting, so a redirect cannot escape the
// private-address guard through DNS rebinding.
func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > c.maxRedirects {
		return ErrTooManyRedirects
	}
	if err := validateURL(req.URL); err != nil {
		return err
	}
	host := strings.TrimSuffix(strings.ToLower(req.URL.Hostname()), ".")
	if parseLiteralIP(host) != nil || c.lookupIP == nil {
		return nil
	}
	ips, err := c.lookupIP(req.Context(), host)
	if err != nil {
		// A DNS failure is reported by the real dial. Do not turn a temporary
		// resolver problem into a permanent redirect policy error.
		return nil
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return fmt.Errorf("%w: %s", ErrBlockedAddress, host)
		}
	}
	return nil
}
