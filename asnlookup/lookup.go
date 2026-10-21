package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// RIPEstat is RIPE NCC's free, public API (one of the 5 regional internet
// registries). No API key needed, and it covers global routing, not just
// the RIPE region. We use it instead of bgpview.io because that service
// shut down permanently (that's why DNS resolution used to fail).
var ripestatBase = "https://stat.ripe.net/data"

var httpClient = &http.Client{Timeout: 12 * time.Second}

type networkInfoResponse struct {
	Status string `json:"status"`
	Data   struct {
		Asns   []string `json:"asns"`
		Prefix string   `json:"prefix"`
	} `json:"data"`
}

type asOverviewResponse struct {
	Status string `json:"status"`
	Data   struct {
		Holder    string `json:"holder"`
		Announced bool   `json:"announced"`
	} `json:"data"`
}

type announcedPrefixesResponse struct {
	Status string `json:"status"`
	Data   struct {
		Prefixes []struct {
			Prefix string `json:"prefix"`
		} `json:"prefixes"`
	} `json:"data"`
}

// OwnerInfo is the result of identifying who owns an IP.
type OwnerInfo struct {
	QueryIP string  `json:"query_ip"`
	ASN     int     `json:"asn"`
	OrgName string  `json:"org_name"`
	Country string  `json:"country"`
	Ranges  []Range `json:"ranges"`
}

// resolveTarget accepts an IP or a URL/hostname and returns a concrete IP.
func resolveTarget(input string) (string, error) {
	in := strings.TrimSpace(input)

	if strings.Contains(in, "://") {
		u, err := url.Parse(in)
		if err == nil && u.Hostname() != "" {
			in = u.Hostname()
		}
	}
	in = strings.TrimSuffix(in, "/")
	if h, _, err := net.SplitHostPort(in); err == nil {
		in = h
	}

	if ip := net.ParseIP(in); ip != nil {
		return ip.String(), nil
	}

	addrs, err := net.LookupHost(in)
	if err != nil || len(addrs) == 0 {
		return "", fmt.Errorf("couldn't resolve %q to an IP: %w", in, err)
	}
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.To4() != nil {
			return ip.String(), nil
		}
	}
	return addrs[0], nil
}

func fetchJSON(url string, out interface{}) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "asnlookup/1.0")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("request to %s returned %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// guessCountry tries to pull the country code off the tail of the holder
// name (e.g. "COGECO-ASN, CA" -> "CA"). Not always present.
func guessCountry(holder string) string {
	parts := strings.Split(holder, ",")
	if len(parts) < 2 {
		return ""
	}
	last := strings.TrimSpace(parts[len(parts)-1])
	if len(last) == 2 && strings.ToUpper(last) == last {
		return last
	}
	return ""
}

// lookupOwner, given an IP, finds the owning ASN/company (via RIPEstat) and
// every range (prefix) that company announces.
func lookupOwner(ip string) (*OwnerInfo, error) {
	var niResp networkInfoResponse
	if err := fetchJSON(fmt.Sprintf("%s/network-info/data.json?resource=%s", ripestatBase, ip), &niResp); err != nil {
		return nil, fmt.Errorf("failed querying RIPEstat (check your internet connection): %w", err)
	}
	if len(niResp.Data.Asns) == 0 {
		return nil, fmt.Errorf("RIPEstat found no ASN announcing %s (is it a private/unrouted IP?)", ip)
	}

	asn, err := strconv.Atoi(niResp.Data.Asns[0])
	if err != nil {
		return nil, fmt.Errorf("unexpected ASN %q: %w", niResp.Data.Asns[0], err)
	}

	var ovResp asOverviewResponse
	orgName := fmt.Sprintf("AS%d", asn)
	if err := fetchJSON(fmt.Sprintf("%s/as-overview/data.json?resource=AS%d", ripestatBase, asn), &ovResp); err == nil {
		if ovResp.Data.Holder != "" {
			orgName = ovResp.Data.Holder
		}
	}

	var apResp announcedPrefixesResponse
	if err := fetchJSON(fmt.Sprintf("%s/announced-prefixes/data.json?resource=AS%d", ripestatBase, asn), &apResp); err != nil {
		return nil, fmt.Errorf("failed querying AS%d's prefixes: %w", asn, err)
	}

	var ranges []Range
	for _, p := range apResp.Data.Prefixes {
		r, err := cidrToRange(p.Prefix)
		if err != nil {
			continue
		}
		ranges = append(ranges, r)
	}
	if len(ranges) == 0 {
		return nil, fmt.Errorf("AS%d (%s) has no announced prefixes right now", asn, orgName)
	}

	return &OwnerInfo{
		QueryIP: ip,
		ASN:     asn,
		OrgName: orgName,
		Country: guessCountry(orgName),
		Ranges:  ranges,
	}, nil
}
