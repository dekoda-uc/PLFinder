# asnlookup

A pure-console Go tool, zero external dependencies: give it an IP or a
URL and it tells you which company (ASN) owns it, using public routing
data (RIPE NCC's RIPEstat API), and lists ALL the IP ranges (IPv4
and/or IPv6) that company announces.

**It never connects to any host belonging to that company, and never
scans any port.** It's purely a public routing-information lookup —
the same kind of thing a `whois` gives you, except it returns every
block of the ASN at once instead of just one.

By default, ranges that are already fully contained inside a bigger
one are collapsed (very common: an ISP announces a /22 AND, separately,
each of its /24s too — typically as BGP anti-hijack protection). Use
`--raw` to see the full, uncollapsed list instead.

## How to use it

**No arguments**: interactive mode, asks questions on the console.

```
./asnlookup
asnlookup — ip or url to look up: 203.0.113.45
ip family to list [4/6/all] (enter = 4):
...
open the results folder? [y/N]:
press enter to exit...
```

**With an argument**: direct mode, to use it as a building block for
another program.

```bash
asnlookup 203.0.113.45
asnlookup --ip-version all --raw https://example.com
```

- Progress goes to **stderr**. The result (JSON) goes to **stdout** and
  is saved to `output/last_lookup.json` (overwritten on every run).
- `--ip-version 4|6|all` controls which IP family is listed (default:
  v4 only).
- `--raw` shows every announced prefix, including ones already
  contained in a bigger block (by default those get collapsed away).
- `asnlookup --help` for all the options.

## Output format

`output/last_lookup.json`:
```json
{
  "timestamp": "2026-10-19T23:33:32Z",
  "query_ip": "203.0.113.45",
  "asn": 64496,
  "org": "EXAMPLE-ISP, ZZ",
  "country": "ZZ",
  "collapsed": true,
  "ranges": [
    {"cidr": "203.0.113.0/24", "family": "v4", "start": "203.0.113.0", "end": "203.0.113.255", "size": 256},
    {"cidr": "2001:db8:abcd::/48", "family": "v6", "start": "2001:db8:abcd::", "end": "2001:db8:abcd:ffff:ffff:ffff:ffff:ffff", "size": 0}
  ]
}
```

`family` is `"v4"` or `"v6"`. For IPv6, `size` is always 0 (not
meaningful for an address space that large). `collapsed` tells you
whether the smaller-ranges-inside-bigger-ones were dropped (`true`,
the default) or kept (`false`, with `--raw`).

## Requirements

- Go 1.22 or newer. Nothing else — zero dependencies, system or Go.
  `go build` is enough on Windows, macOS, or Linux.

## Building

```bash
cd asnlookup
go build -o asnlookup .          # on Windows this alone produces asnlookup.exe
```
