package main

import (
	"encoding/json"
	"time"
)

// LookupReport is the JSON that ends up in output/last_lookup.json: the
// company that owns the queried IP and all of its announced ranges.
type LookupReport struct {
	Timestamp string  `json:"timestamp"`
	QueryIP   string  `json:"query_ip"`
	ASN       int     `json:"asn"`
	Org       string  `json:"org"`
	Country   string  `json:"country"`
	Collapsed bool    `json:"collapsed"`
	Ranges    []Range `json:"ranges"`
}

func buildLookupJSON(info *OwnerInfo, collapsed bool) ([]byte, error) {
	r := LookupReport{
		Timestamp: time.Now().Format(time.RFC3339),
		QueryIP:   info.QueryIP,
		ASN:       info.ASN,
		Org:       info.OrgName,
		Country:   info.Country,
		Collapsed: collapsed,
		Ranges:    info.Ranges,
	}
	return json.MarshalIndent(r, "", "  ")
}
