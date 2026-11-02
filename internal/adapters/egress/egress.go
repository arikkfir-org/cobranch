// Package egress keeps the controller's own connections out of the address ranges runs may not reach, unless the
// operator allowed them: webhooks' receivers and other clusters' API servers. It checks each address it dials, rather
// than the name it resolved, so a name that resolves into a refused range is refused too.
package egress

import (
	"fmt"
	"net"
	"net/netip"
	"syscall"
	"time"
)

// Prefixes parses CIDRs.
func Prefixes(cidrs []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			return nil, fmt.Errorf("range %q: %w", c, err)
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

// Guard refuses connections to an address in Deny and not in Allow, and to an unspecified one, with Refused.
type Guard struct {
	Deny, Allow []netip.Prefix
	// Refused is the error a refusal wraps, which its callers tell it by.
	Refused error
}

// Check refuses an address the Guard may not connect to.
func (g Guard) Check(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("reading address %q: %w", address, err)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("reading address %q: %w", address, err)
	}
	ip = ip.Unmap()
	if ip.IsUnspecified() {
		return fmt.Errorf("%w: %s", g.Refused, ip)
	}
	for _, p := range g.Allow {
		if p.Contains(ip) {
			return nil
		}
	}
	for _, p := range g.Deny {
		if p.Contains(ip) {
			return fmt.Errorf("%w: %s, in %s", g.Refused, ip, p)
		}
	}
	return nil
}

// Dialer connects within timeout, checking each address it dials.
func (g Guard) Dialer(timeout time.Duration) *net.Dialer {
	return &net.Dialer{Timeout: timeout, Control: func(_, address string, _ syscall.RawConn) error {
		return g.Check(address)
	}}
}
