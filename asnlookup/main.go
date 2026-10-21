package main

import (
	"fmt"
	"os"
	"strings"
)

func normalizeIPVersion(s string) string {
	switch strings.ToLower(strings.TrimPrefix(s, "v")) {
	case "4":
		return "v4"
	case "6":
		return "v6"
	default:
		return "all"
	}
}

func printUsage() {
	fmt.Println(`asnlookup — identifies which company owns an IP/URL and lists its announced ranges.

It never connects to any host or scans any port: it only queries public
routing information (RIPEstat) and gives you the list of IPv4/IPv6 ranges
for that company — like a whois, but returning every block of the ASN.

Usage:
  asnlookup                       interactive mode: asks questions on the console
  asnlookup [options] <ip-or-url> direct mode (to use it as a building block for another program)

Options (go BEFORE the ip/url):
  --ip-version 4|6|all   which IP family to list (default: 4)
  --raw                  don't collapse: show every prefix exactly as the
                         company announces it, including ones that are
                         already contained in a bigger one (by default
                         those are dropped, since they're usually the same
                         address space announced twice at different
                         granularity)

In direct mode: progress goes to stderr, and the result (JSON) is printed
to stdout and also saved to output/last_lookup.json.`)
}

func main() {
	os.MkdirAll("output", 0755)

	ipVersion := "v4"
	showAll := false
	var target string

	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--ip-version", "-ip-version":
			i++
			if i < len(args) {
				ipVersion = normalizeIPVersion(args[i])
			}
		case "--raw", "-raw":
			showAll = true
		case "--help", "-h":
			printUsage()
			return
		default:
			if target == "" {
				target = args[i]
			}
		}
	}

	if target != "" {
		if err := runCLI(target, ipVersion, showAll); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}

	runInteractive(ipVersion, showAll)
}
