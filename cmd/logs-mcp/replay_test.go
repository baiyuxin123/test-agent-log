package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Optional local replay; private logs stay outside the repository and test output.
func TestHistoricalReplay(t *testing.T) {
	dir, reference := os.Getenv("MCP_REPLAY_CACHE"), os.Getenv("MCP_REPLAY_REFERENCE")
	if dir == "" || reference == "" {
		t.Skip("set MCP_REPLAY_CACHE and MCP_REPLAY_REFERENCE for private historical replay")
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	rows := []evidenceRow{}
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		d := decodeObject(string(b))
		receipt := objectValue(d["receipt"])
		project := str(receipt["project"])
		for _, v := range arrayValue(d["rows"]) {
			m := objectValue(v)
			if m == nil {
				continue
			}
			content, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			rows = append(rows, evidenceRow{project, map[string]string{"content": string(content), "__time__": str(m["unix_time"])}})
		}
	}
	b, err := os.ReadFile(reference)
	if err != nil {
		t.Fatal(err)
	}
	records := arrayValue(decodeObject(string(b))["records"])
	checked := 0
	incompleteAmounts := 0
	for sourceIndex, v := range records {
		r := objectValue(v)
		initial, latest := objectValue(r["initial"]), objectValue(r["latest"])
		if initial == nil || latest == nil {
			continue
		}
		h := buildPriceHistory(str(r["order_sn"]), rows, true)
		q, _ := intValue(initial["total"])
		last, _ := intValue(latest["total"])
		if h.InitialQuote == nil && h.Latest != nil {
			missingAmount := false
			for _, row := range rows {
				p := decodeObject(row.Row["content"])
				if str(p["Orders"]) != "获取物流包裹参数" || str(p["trace_id"]) != str(initial["trace"]) || str(p["span_id"]) != str(initial["span"]) {
					continue
				}
				quote := objectValue(p["result"])
				for _, v := range arrayValue(quote["goods_quotation"]) {
					if _, ok := objectValue(v)["goods_amount"]; !ok {
						missingAmount = true
					}
				}
				for _, v := range objectValue(quote["agency_goods_logistics_quotation"]) {
					for _, p := range arrayValue(objectValue(v)["goods_items"]) {
						if _, ok := objectValue(p)["logistics_amount"]; !ok {
							missingAmount = true
						}
					}
				}
			}
			if missingAmount && h.Classification == "insufficient_evidence" && h.Latest.Total == last {
				incompleteAmounts++
				continue
			}
		}
		if h.InitialQuote == nil || h.Latest == nil {
			t.Fatalf("record %d: missing quotation or snapshot; gaps=%v", sourceIndex, h.MissingEvidence)
		}
		if h.InitialQuote.Total != q || h.Latest.Total != last {
			t.Fatalf("record %d: amount differs from independently audited historical output", checked)
		}
		expected := "not_observed"
		if r["match"] == true {
			expected = "matches"
		} else if r["historical_match"] == true {
			expected = "historical_match_recovered"
		}
		if h.Classification != expected {
			t.Fatalf("record %d classification=%s want=%s gaps=%v", checked, h.Classification, expected, h.MissingEvidence)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no replay records were verified")
	}
	t.Logf("verified %d historical orders; %d references conservatively incomplete because a quote amount is omitted", checked, incompleteAmounts)
}
