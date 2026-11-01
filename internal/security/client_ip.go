package security

import (
	"errors"
	"net/netip"
	"slices"
	"strings"
)

var errClientCIDRs = errors.New("invalid client IP allowlist")

// NormalizeClientCIDRs bounds and canonicalizes a customer supplied allowlist.
// The returned empty slice means unrestricted access, including legacy keys.
func NormalizeClientCIDRs(values []string) ([]string, error) {
	if len(values) > 32 {
		return nil, errClientCIDRs
	}
	total := 0
	for _, value := range values {
		if len(value) > 4096-total {
			return nil, errClientCIDRs
		}
		total += len(value)
	}
	canonical := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, errClientCIDRs
		}
		var prefix netip.Prefix
		var err error
		if strings.Contains(value, "/") {
			prefix, err = netip.ParsePrefix(value)
		} else {
			var address netip.Addr
			address, err = netip.ParseAddr(value)
			if err == nil && address.Zone() == "" {
				prefix = netip.PrefixFrom(address, address.BitLen())
			}
		}
		if err != nil || !prefix.IsValid() || prefix.Addr().Zone() != "" || prefix.Addr().Is4In6() || prefix.Bits() == 0 {
			return nil, errClientCIDRs
		}
		canonical = append(canonical, prefix.Masked().String())
	}
	slices.Sort(canonical)
	return slices.Compact(canonical), nil
}

// NormalizeClientIP accepts only an address, never a forwarding chain or port.
// IPv4 mapped transport addresses use the same identity as ordinary IPv4.
func NormalizeClientIP(value string) string {
	address, err := netip.ParseAddr(value)
	if err != nil || address.Zone() != "" {
		return ""
	}
	return address.Unmap().String()
}

// ClientIPAllowed also validates persisted configuration before granting access.
// A matching early entry cannot hide a malformed later entry.
func ClientIPAllowed(value string, cidrs []string) bool {
	if len(cidrs) == 0 {
		return true
	}
	if len(cidrs) > 32 {
		return false
	}
	address, err := netip.ParseAddr(value)
	if err != nil || address.Zone() != "" {
		return false
	}
	address = address.Unmap()
	allowed := false
	total := 0
	for _, value := range cidrs {
		if len(value) > 4096-total {
			return false
		}
		total += len(value)
		prefix, err := netip.ParsePrefix(value)
		if err != nil || prefix.Bits() == 0 || prefix.Addr().Is4In6() || prefix != prefix.Masked() || prefix.String() != value {
			return false
		}
		allowed = allowed || prefix.Contains(address)
	}
	return allowed
}
