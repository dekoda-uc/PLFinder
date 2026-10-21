package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// runCLI resolves the target, looks up who announces it (RIPEstat) and its
// ranges, and writes/prints the result. It never connects to any host that
// belongs to the company: it's only a query against public routing
// information.
//
// By default (showAll=false) it collapses ranges that are already fully
// contained inside a bigger one (very common: an ISP announces a /22 AND
// also each of its /24s separately, as BGP anti-hijack protection). With
// showAll=true the full list is shown exactly as RIPEstat returns it.
func runCLI(target string, ipVersion string, showAll bool) error {
	os.MkdirAll("output", 0755)

	fmt.Fprintf(os.Stderr, "Resolving %q...\n", target)
	ip, err := resolveTarget(target)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "IP: %s\n", ip)

	fmt.Fprintln(os.Stderr, "Looking up ASN and ranges (RIPEstat)...")
	info, err := lookupOwner(ip)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Company: %s (AS%d) — %d ranges\n", info.OrgName, info.ASN, len(info.Ranges))

	filtered := *info
	filtered.Ranges = filterRangesByFamily(info.Ranges, ipVersion)
	collapsed := !showAll
	if collapsed {
		before := len(filtered.Ranges)
		filtered.Ranges = collapseRanges(filtered.Ranges)
		if dropped := before - len(filtered.Ranges); dropped > 0 {
			fmt.Fprintf(os.Stderr, "%d range(s) dropped for being contained in a bigger one (use --raw to see them all)\n", dropped)
		}
	}

	data, err := buildLookupJSON(&filtered, collapsed)
	if err != nil {
		return fmt.Errorf("couldn't build the JSON: %w", err)
	}

	path := filepath.Join("output", "last_lookup.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("couldn't write %s: %w", path, err)
	}
	fmt.Fprintf(os.Stderr, "Saved to %s\n\n", path)

	// The final result goes to stdout: that's what another program should
	// capture (or it can just read output/last_lookup.json directly).
	os.Stdout.Write(data)
	fmt.Println()
	return nil
}
