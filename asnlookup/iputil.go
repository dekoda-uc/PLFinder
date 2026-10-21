package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
)

// ip2long converts an IPv4 address to a 32-bit integer.
func ip2long(ip net.IP) uint32 {
	ip4 := ip.To4()
	if ip4 == nil {
		return 0
	}
	return binary.BigEndian.Uint32(ip4)
}

// long2ip converts a 32-bit integer to a net.IP.
func long2ip(n uint32) net.IP {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, n)
	return net.IPv4(b[0], b[1], b[2], b[3])
}

// Range represents a CIDR block already resolved to its first/last address.
// Family is "v4" or "v6" — it matters because IPv6 can NOT be enumerated
// host by host like IPv4 (a single /64 already has 2^64 addresses), so it's
// only ever used as informational reference, never to scan.
type Range struct {
	CIDR   string `json:"cidr"`
	Family string `json:"family"`
	Start  string `json:"start"`
	End    string `json:"end"`
	Size   uint64 `json:"size"`
}

// cidrToRange converts a CIDR into its first/last address. For IPv4 it
// gives the usual "start - end"; for IPv6 it computes the last address at
// the byte level only to display it, not to iterate over it.
func cidrToRange(cidr string) (Range, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return Range{}, fmt.Errorf("invalid cidr %q: %w", cidr, err)
	}

	if v4 := ipnet.IP.To4(); v4 != nil {
		ones, bits := ipnet.Mask.Size()
		hostBits := bits - ones
		start := ip2long(ipnet.IP)
		size := uint64(1) << uint(hostBits)
		end := start + uint32(size-1)
		return Range{
			CIDR: cidr, Family: "v4",
			Start: long2ip(start).String(), End: long2ip(end).String(),
			Size: size,
		}, nil
	}

	// IPv6: never enumerated host by host, so Size is left at 0 on
	// purpose (that's the signal for anything downstream to ignore it).
	ip16 := ipnet.IP.To16()
	if ip16 == nil {
		return Range{}, fmt.Errorf("unexpected IP address in %q", cidr)
	}
	ones, bits := ipnet.Mask.Size()
	hostBits := bits - ones
	end := make(net.IP, len(ip16))
	copy(end, ip16)
	for i := 0; i < hostBits; i++ {
		byteIdx := len(end) - 1 - i/8
		bitIdx := uint(i % 8)
		end[byteIdx] |= 1 << bitIdx
	}
	return Range{CIDR: cidr, Family: "v6", Start: ipnet.IP.String(), End: end.String(), Size: 0}, nil
}

// ipCompare compares two IPs (same family) so ranges can be ordered/checked
// for containment without relying on uint32 — this way it works for both
// v4 and v6.
func ipCompare(a, b net.IP) int {
	if a4, b4 := a.To4(), b.To4(); a4 != nil && b4 != nil {
		return bytes.Compare(a4, b4)
	}
	return bytes.Compare(a.To16(), b.To16())
}

// collapseRanges drops ranges that are already fully contained inside
// another, bigger range (same family). It's very common for an ISP to
// announce a big block AND, separately, each of its smaller pieces
// (typically as BGP route anti-hijack protection) — without this, the list
// ends up full of blocks that are, in practice, "the same" address space
// repeated at different granularity.
func collapseRanges(ranges []Range) []Range {
	type parsed struct {
		r          Range
		start, end net.IP
		ok         bool
	}
	items := make([]parsed, len(ranges))
	for i, r := range ranges {
		s, e := net.ParseIP(r.Start), net.ParseIP(r.End)
		items[i] = parsed{r: r, start: s, end: e, ok: s != nil && e != nil}
	}

	var out []Range
	for i, it := range items {
		if !it.ok {
			out = append(out, it.r) // couldn't be parsed, keep as is
			continue
		}
		redundant := false
		for j, other := range items {
			if i == j || !other.ok || it.r.Family != other.r.Family {
				continue
			}
			contained := ipCompare(other.start, it.start) <= 0 && ipCompare(it.end, other.end) <= 0
			if !contained {
				continue
			}
			// it is inside other. If other is strictly bigger, it is
			// redundant. If they're the same size (rare case, same
			// range under a different CIDR), keep the one with the
			// lower index so nothing is duplicated or lost.
			if other.r.Size > it.r.Size || (other.r.Size == it.r.Size && j < i) {
				redundant = true
				break
			}
		}
		if !redundant {
			out = append(out, it.r)
		}
	}
	return out
}

// filterRangesByFamily filters by "v4", "v6" or "all" (any other value is
// treated as "all").
func filterRangesByFamily(ranges []Range, family string) []Range {
	if family != "v4" && family != "v6" {
		return ranges
	}
	var out []Range
	for _, r := range ranges {
		if r.Family == family {
			out = append(out, r)
		}
	}
	return out
}
