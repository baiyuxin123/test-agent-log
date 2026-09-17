package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Catch mistaking a completed SLS page for an exhausted result set.
func TestCollectAllExhaustsPages(t *testing.T) {
	fetch := func(_ context.Context, a slsQueryArgs) (*slsQueryResult, error) {
		rows := []map[string]string{}
		if a.Offset == 0 {
			for i := 0; i < 100; i++ {
				rows = append(rows, map[string]string{"content": fmt.Sprintf(`{"id":%d}`, i)})
			}
		} else if a.Offset == 100 {
			rows = append(rows, map[string]string{"content": `{"id":100}`})
		}
		return &slsQueryResult{Completed: true, Rows: rows}, nil
	}
	r, err := collectAll(context.Background(), fetch, collectArgs{SLS: slsQueryArgs{Query: "x", FromTime: 10, ToTime: 20}, MaxRequests: 10})
	if err != nil || !r.AllPagesFetched || len(r.Rows) != 101 || r.Requests != 2 {
		t.Fatalf("unexpected collection: %+v %v", r, err)
	}
}
func TestCollectAllBudgetAndSplit(t *testing.T) {
	fetch := func(_ context.Context, a slsQueryArgs) (*slsQueryResult, error) {
		if a.ToTime-a.FromTime > 1 {
			return &slsQueryResult{Completed: false}, nil
		}
		return &slsQueryResult{Completed: true, Rows: []map[string]string{{"__time__": fmt.Sprint(a.FromTime)}}}, nil
	}
	r, err := collectAll(context.Background(), fetch, collectArgs{SLS: slsQueryArgs{Query: "x", FromTime: 10, ToTime: 12}, MaxRequests: 10})
	if err != nil || !r.AllPagesFetched || len(r.Rows) != 2 {
		t.Fatalf("split: %+v %v", r, err)
	}
	r, err = collectAll(context.Background(), fetch, collectArgs{SLS: slsQueryArgs{Query: "x", FromTime: 10, ToTime: 12}, MaxRequests: 1})
	if err != nil || r.AllPagesFetched || len(r.MissingEvidence) == 0 {
		t.Fatalf("budget must report partial: %+v %v", r, err)
	}
}
func TestCollectRejectsAnalysisAndInvalidWindow(t *testing.T) {
	for _, a := range []slsQueryArgs{{Query: "* | select count(*)", FromTime: 1, ToTime: 2}, {Query: "x", FromTime: 2, ToTime: 1}} {
		_, err := collectAll(context.Background(), nil, collectArgs{SLS: a})
		if err == nil {
			t.Fatal("expected validation error")
		}
	}
}
func testLog(project, ts string, p map[string]any) evidenceRow {
	b, _ := json.Marshal(p)
	return evidenceRow{Project: project, Row: map[string]string{"content": string(b), "__time__": ts}}
}
func snapshot(id, ts string, goods, freight, adjustment, total int) evidenceRow {
	s := map[string]any{"order": map[string]any{"thirdOrderId": id, "dsersOrderId": "900", "status": "WAIT_BUYER_PAY", "productTotalFee": goods, "shippingAmount": map[string]any{"amount": freight}, "otherAmount": map[string]any{"amount": adjustment}, "amount": map[string]any{"amount": total, "currencyCode": "USD"}}}
	b, _ := json.Marshal(s)
	q, _ := json.Marshal(string(b))
	return testLog("dsers", ts, map[string]any{"SaveOrderSnapshot": "order_sn: " + id + " snapshot_json:" + string(q), "trace_id": "snap"})
}

// Differentiate first persisted amount from quotation; reject unrelated rule targets.
func TestPriceHistoryStagesAndEvidence(t *testing.T) {
	rows := []evidenceRow{snapshot("123", "20", 2300, 620, 0, 2920), snapshot("123", "30", 2300, 620, 0, 2920), snapshot("999", "40", 1, 1, 0, 2), testLog("dsers", "25", map[string]any{"msg": "executeTaskSuccess", "target_id": "999", "apply_info": `[{"rule_id":"wrong","amount_before":3234,"amount_after":2920}]`})}
	h := buildPriceHistory("123", rows, true)
	if h.FirstSaved == nil || h.FirstSaved.Total != 2920 || h.Latest.Total != 2920 || len(h.Changes) != 1 || len(h.Rules) != 0 {
		t.Fatalf("bad history %+v", h)
	}
	if h.Classification != "insufficient_evidence" || h.RuleEvidence != "not_found_in_scope" {
		t.Fatalf("missing quote must not imply no change: %+v", h)
	}
	h.InitialQuote = &moneyEvent{Goods: 2300, Freight: 934, Total: 3234, Currency: "USD"}
	classifyPriceHistory(&h)
	if h.Classification != "matches" || !h.FirstSaveDiffers || h.PostSaveChanges != 0 {
		t.Fatalf("wrong stages: %+v", h)
	}
	h.AllPagesFetched = false
	classifyPriceHistory(&h)
	if h.Classification != "insufficient_evidence" {
		t.Fatal("partial data classified conclusively")
	}
}
func TestInvalidSnapshotNotSilentlyAccepted(t *testing.T) {
	h := buildPriceHistory("123", []evidenceRow{snapshot("123", "20", 100, 100, 0, 999)}, true)
	if h.Latest != nil || len(h.MissingEvidence) == 0 {
		t.Fatalf("invalid sum accepted %+v", h)
	}
}
func TestEmailSameObjectAndLargeIDs(t *testing.T) {
	rows := []evidenceRow{testLog("dsers", "1", map[string]any{"content": `{"userId":9007199254740993123,"email":"right@example.com","token":"SECRET","child":{"email":"wrong@example.com"}}`}), testLog("dsers", "2", map[string]any{"userId": "other", "email": "wrong@example.com"})}
	r := resolveEmailsFromRows([]string{"9007199254740993123", "missing", "9007199254740993123"}, rows, true)
	if len(r) != 3 || r[0].Status != "resolved" || r[0].Email != "right@example.com" || r[1].Status != "not_found_in_scope" || r[2].Email != r[0].Email {
		t.Fatalf("bad resolution %+v", r)
	}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "SECRET") || strings.Contains(string(b), "wrong@example.com") {
		t.Fatal("unrelated data leaked")
	}
	rows = append(rows, testLog("dsers", "3", map[string]any{"id": "9007199254740993123", "email": "second@example.com"}))
	r = resolveEmailsFromRows([]string{"9007199254740993123"}, rows, true)
	if r[0].Status != "conflict" || r[0].Email != "" {
		t.Fatal("conflict hidden")
	}
}
func TestSanitizeNestedCredentials(t *testing.T) {
	v := decodeObject(`{"args":"{\"token\":\"SECRET\",\"userId\":\"123\"}","headers":{"Authorization":"Bearer SECRET2"},"password":"SECRET3"}`)
	b, _ := json.Marshal(sanitizeValue(v))
	if strings.Contains(string(b), "SECRET") {
		t.Fatalf("secret leaked %s", b)
	}
}

func TestQuoteCorrelatesTraceSpanAndItems(t *testing.T) {
	rows := []evidenceRow{
		testLog("dsers", "10", map[string]any{"CreateOrder": "返回", "trace_id": "t1", "span_id": "s1", "result": `{"dsers_order_id":"900","order_list":[{"order_id":"123"}]}`}),
		testLog("dsers", "10", map[string]any{"operation": "/OrderService/CreateOrder", "trace_id": "t1", "args": `dsers_user_id:777 dsers_order_name:"#12" line_items:{dsers_line_item_id:1 product_id:"p" variant_id:"v" quantity:2} address:{country:"US" zip:"12345"}`, "reply": `order_list:{order_id:"123"}`}),
		testLog("dsers", "9", map[string]any{"Orders": "获取物流包裹参数", "trace_id": "t1", "span_id": "s1", "req": `{"ship_country":"US","zip":"12345","goods_items":[{"virtually_product_id":"p","virtually_variant_id":"v","count":2}]}`, "result": `{"goods_quotation":[{"goods_amount":2300}],"agency_goods_logistics_quotation":{"a1":{"goods_items":[{"logistics_amount":934}]}}}`}),
		snapshot("123", "20", 2300, 620, 0, 2920),
	}
	h := buildPriceHistory("123", rows, true)
	if h.InitialQuote == nil || h.InitialQuote.Total != 3234 || h.Classification != "matches" {
		t.Fatalf("quote missing: %+v", h)
	}
	// The ingress trace may differ; exact returned order plus DSers ID still binds it.
	rows[1] = testLog("dsers", "10", map[string]any{"operation": "/OrderService/CreateOrder", "trace_id": "ingress", "args": `dsers_order_id:900 dsers_user_id:777 line_items:{dsers_line_item_id:1 product_id:"p" variant_id:"v" quantity:2} address:{country:"US" zip:"12345"}`, "reply": `order_list:{order_id:"123"}`})
	h = buildPriceHistory("123", rows, true)
	if h.InitialQuote == nil {
		t.Fatal("exact cross-trace creation identity lost")
	}
	// A different item in the same trace must not be accepted as this order's quote.
	bad := testLog("dsers", "9", map[string]any{"Orders": "获取物流包裹参数", "trace_id": "t1", "span_id": "s1", "req": `{"ship_country":"US","zip":"12345","goods_items":[{"virtually_product_id":"other","virtually_variant_id":"v","count":2}]}`, "result": `{"goods_quotation":[{"goods_amount":2300}],"agency_goods_logistics_quotation":{"a1":{"goods_items":[{"logistics_amount":934}]}}}`})
	rows[2] = bad
	h = buildPriceHistory("123", rows, true)
	if h.InitialQuote != nil {
		t.Fatal("unrelated quote accepted")
	}
}

func TestEvidenceUsesEventMilliseconds(t *testing.T) {
	r := testLog("dsers", "1789615210", map[string]any{"ts": "2026-09-17T11:20:10.321+08:00"})
	e := rowEvidence(r)
	if e.UnixMilli != 1789615210321 {
		t.Fatalf("lost event precision %d", e.UnixMilli)
	}
}
func TestRuleMissingAmountsAreNotZero(t *testing.T) {
	r := testLog("dsers", "10", map[string]any{"msg": "executeTaskSuccess", "target_id": "123", "apply_info": `[{"rule_id":"R"}]`})
	h := buildPriceHistory("123", []evidenceRow{r}, true)
	if len(h.Rules) != 1 || h.Rules[0].Before != nil || h.Rules[0].After != nil || h.Rules[0].SavedMatch != "not_verified" {
		t.Fatalf("invented amount %+v", h)
	}
}
func TestBatchKeepsInputOrderAndCancellation(t *testing.T) {
	a := businessArgs{OrderSNs: []string{"2", "1", "2"}, FromTime: 10, ToTime: 20, Concurrency: 2}
	if err := a.validate("order_price_audit"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := runOrderBatch(ctx, nil, a)
	if len(r) != 3 || r[0].OrderSN != "2" || r[1].OrderSN != "1" || r[2].OrderSN != "2" {
		t.Fatalf("order changed %+v", r)
	}
	for _, h := range r {
		if h.AllPagesFetched || h.Classification != "insufficient_evidence" {
			t.Fatal("cancelled batch reported complete")
		}
	}
}
func TestToolArgumentsRejectNumericIDsBeforeNetwork(t *testing.T) {
	s := &mcpServer{}
	for _, raw := range []string{`{"name":"order_price_history","arguments":{"order_sn":123}}`, `{"name":"order_price_audit","arguments":{"order_sns":["123 or *"]}}`, `{"name":"order_context","arguments":{"order_sns":["123"],"fields":["password"]}}`} {
		r, err := s.callTool(context.Background(), json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		m := r.(map[string]any)
		if m["isError"] != true {
			t.Fatalf("invalid args accepted: %+v", m)
		}
	}
}
func TestSameTimeConflictPreventsPriceConclusion(t *testing.T) {
	h := buildPriceHistory("123", []evidenceRow{snapshot("123", "20", 100, 100, 0, 200), snapshot("123", "20", 100, 50, 0, 150)}, true)
	h.InitialQuote = &moneyEvent{Goods: 100, Freight: 100, Total: 200, Currency: "USD"}
	classifyPriceHistory(&h)
	if h.Classification != "insufficient_evidence" {
		t.Fatal("same-time conflict ignored")
	}
}

func TestCollectRowBudgetDoesNotClaimAllPages(t *testing.T) {
	fetch := func(_ context.Context, a slsQueryArgs) (*slsQueryResult, error) {
		rows := make([]map[string]string, 100)
		for i := range rows {
			rows[i] = map[string]string{"content": `{}`}
		}
		return &slsQueryResult{Completed: true, Rows: rows}, nil
	}
	r, err := collectAll(context.Background(), fetch, collectArgs{SLS: slsQueryArgs{Query: "x", FromTime: 1, ToTime: 2}, MaxRows: 10})
	if err != nil || r.AllPagesFetched || len(r.Rows) != 10 || len(r.MissingEvidence) == 0 {
		t.Fatalf("row limit misreported %+v %v", r, err)
	}
}
func TestProjectionUsesPersistedTimeAndSellerID(t *testing.T) {
	r := snapshot("123", "20", 100, 20, 0, 120)
	p := decodeObject(r.Row["content"])
	q := snapshotPattern.FindStringSubmatch(str(p["SaveOrderSnapshot"]))
	var s string
	json.Unmarshal([]byte(q[1]), &s)
	d := decodeObject(s)
	o := objectValue(d["order"])
	o["createdTime"] = "2026-09-10T10:00:00+08:00"
	o["outerOrderId"] = "700"
	b, _ := json.Marshal(d)
	quoted, _ := json.Marshal(string(b))
	p["SaveOrderSnapshot"] = "order_sn: 123 snapshot_json:" + string(quoted)
	r = testLog("dsers", "20", p)
	h := buildPriceHistory("123", []evidenceRow{r}, true)
	if h.Fields["created_at"].Value != "2026-09-10T10:00:00+08:00" || h.Fields["seller_order_id"].Value != "700" {
		t.Fatalf("context fields lost %+v", h.Fields)
	}
}

func TestManualPriceUpdateKeepsExactOrderAndMissingPrice(t *testing.T) {
	r := testLog("dsers", "10", map[string]any{"operation": "/OrderService/BatchUpdateOrderPrice", "req": `order_list:{order_sn:999 price:100 operator:"="} order_list:{order_sn:123 price:2510 operator:"="}`, "reply": `code:0`})
	h := buildPriceHistory("123", []evidenceRow{r}, true)
	if len(h.Updates) != 1 || h.Updates[0].Price == nil || *h.Updates[0].Price != 2510 || h.Updates[0].Operator != "=" {
		t.Fatalf("update attribution incorrect %+v", h.Updates)
	}
	r = testLog("dsers", "11", map[string]any{"operation": "/OrderService/BatchUpdateOrderPrice", "args": `order_list:{order_sn:123 operator:"="}`})
	h = buildPriceHistory("123", []evidenceRow{r}, true)
	if len(h.Updates) != 1 || h.Updates[0].Price != nil {
		t.Fatal("missing requested price treated as zero")
	}
}

func TestOrderQueriesScopeOperationalEvents(t *testing.T) {
	fetch := func(_ context.Context, a slsQueryArgs) (*slsQueryResult, error) {
		if !strings.Contains(a.Query, "SaveOrderSnapshot and ORDER_CHANGE") || !strings.Contains(a.Query, "BatchUpdateOrderPrice") {
			return nil, fmt.Errorf("broad query includes unrelated list payloads")
		}
		return &slsQueryResult{Completed: true, Project: a.ProjectAlias, Rows: []map[string]string{snapshot("123", "20", 100, 20, 0, 120).Row}}, nil
	}
	a := businessArgs{OrderSN: "123", FromTime: 1, ToTime: 30}
	if err := a.validate("order_price_history"); err != nil {
		t.Fatal(err)
	}
	h := investigateOrder(context.Background(), fetch, a, "123")
	if !h.AllPagesFetched || h.Latest == nil {
		t.Fatalf("scoped retrieval failed: %v", h.MissingEvidence)
	}
}

func TestExplicitProjectPreservedForTraceEnrichment(t *testing.T) {
	fetch := func(_ context.Context, a slsQueryArgs) (*slsQueryResult, error) {
		if a.Project != projectDianShi {
			return nil, fmt.Errorf("wrong project")
		}
		return &slsQueryResult{Completed: true, Rows: []map[string]string{{"content": `{"userId":"123"}`}}}, nil
	}
	i := investigation{fetch: fetch, args: businessArgs{FromTime: 1, ToTime: 2, MaxRequests: 10, MaxRows: 100}, complete: true}
	i.query(context.Background(), projectDianShi, `"trace"`)
	if !i.complete || len(i.rows) != 1 || i.rows[0].Project != projectDianShi {
		t.Fatal("trace enrichment silently routed to default project")
	}
}
