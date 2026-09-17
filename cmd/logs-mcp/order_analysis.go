package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type evidenceRow struct {
	Project string
	Row     map[string]string
}
type evidence struct {
	Project   string `json:"project"`
	Time      string `json:"time"`
	UnixMilli int64  `json:"unix_milli"`
	Trace     string `json:"trace_id,omitempty"`
	Span      string `json:"span_id,omitempty"`
}

func rowEvidence(r evidenceRow) evidence {
	n, t := rowTimestampMillis(r.Row, r.Row["content"])
	p := decodeObject(r.Row["content"])
	if event, err := time.Parse(time.RFC3339Nano, str(p["ts"])); err == nil {
		n = event.UnixMilli()
		t = event.In(beijingLocation).Format("2006-01-02 15:04:05.000 MST")
	}
	return evidence{r.Project, t, n, str(p["trace_id"]), str(p["span_id"])}
}

type moneyEvent struct {
	evidence
	Kind       string `json:"kind"`
	Goods      int64  `json:"goods_minor"`
	Freight    int64  `json:"freight_minor"`
	Adjustment int64  `json:"adjustment_minor"`
	Total      int64  `json:"total_minor"`
	Currency   string `json:"currency"`
	Status     string `json:"status,omitempty"`
}
type ruleEvent struct {
	evidence
	RuleID      string `json:"rule_id"`
	ActionType  string `json:"action_type,omitempty"`
	ActionValue any    `json:"action_value,omitempty"`
	Before      *int64 `json:"before_minor"`
	After       *int64 `json:"after_minor"`
	SavedMatch  string `json:"saved_match"`
}
type priceUpdate struct {
	evidence
	Price        *int64 `json:"requested_price_minor"`
	Operator     string `json:"operator"`
	ResponseCode string `json:"response_code,omitempty"`
	Kind         string `json:"kind"`
}

var updateOrderPattern = regexp.MustCompile(`order_list:\s*\{([^{}]*)\}`)

func extractPriceUpdates(p map[string]any, id string, e evidence) []priceUpdate {
	out := []priceUpdate{}
	if !strings.Contains(str(p["operation"]), "BatchUpdateOrderPrice") && str(p["BatchUpdateOrderPrice"]) == "" {
		return out
	}
	raw := payloadString(p["req"])
	if raw == "null" || raw == "" {
		raw = payloadString(p["args"])
	}
	for _, m := range updateOrderPattern.FindAllStringSubmatch(raw, -1) {
		if protoField(m[1], "order_sn") != id {
			continue
		}
		code := str(p["code"])
		if code == "" {
			code = protoField(payloadString(p["reply"]), "code")
		}
		out = append(out, priceUpdate{evidence: e, Price: intPointer(protoField(m[1], "price")), Operator: protoField(m[1], "operator"), ResponseCode: code, Kind: "update_request_not_proof_of_persistence"})
	}
	return out
}

type fieldEvidence struct {
	Value string `json:"value"`
	evidence
}
type priceHistory struct {
	Updates          []priceUpdate            `json:"price_update_requests"`
	OrderSN          string                   `json:"order_sn"`
	InitialQuote     *moneyEvent              `json:"initial_quote"`
	FirstSaved       *moneyEvent              `json:"first_saved"`
	Latest           *moneyEvent              `json:"latest_log_snapshot"`
	Changes          []moneyEvent             `json:"saved_price_changes"`
	Rules            []ruleEvent              `json:"rule_events"`
	SnapshotCount    int                      `json:"snapshot_count"`
	FirstSaveDiffers bool                     `json:"first_save_differs_from_quote"`
	PostSaveChanges  int                      `json:"post_save_changes"`
	Classification   string                   `json:"classification"`
	HistoricalMatch  bool                     `json:"historical_match"`
	RuleEvidence     string                   `json:"rule_evidence"`
	AllPagesFetched  bool                     `json:"all_pages_fetched"`
	MissingEvidence  []string                 `json:"missing_evidence"`
	Fields           map[string]fieldEvidence `json:"fields"`
	Coverage         []coverage               `json:"coverage"`
}

func intValue(v any) (int64, bool) {
	s := str(v)
	if s == "" {
		return 0, false
	}
	n, e := strconv.ParseInt(s, 10, 64)
	return n, e == nil
}
func intPointer(v any) *int64 {
	n, ok := intValue(v)
	if !ok {
		return nil
	}
	return &n
}

var snapshotPattern = regexp.MustCompile(`snapshot_json:("(?:\\.|[^"\\])*")`)
var snapshotIDPattern = regexp.MustCompile(`\border_sn:\s*(\d+)\b`)
var itemPattern = regexp.MustCompile(`line_items:\s*\{\s*dsers_line_item_id:\s*\d+\s+product_id:\s*"([^"]+)"\s+variant_id:\s*"([^"]+)"\s+quantity:\s*(\d+)`)

func protoField(s, key string) string {
	m := regexp.MustCompile(`\b` + regexp.QuoteMeta(key) + `:\s*(?:"([^"\r\n]*)"|([A-Za-z0-9_.-]+))`).FindStringSubmatch(s)
	if len(m) == 0 {
		return ""
	}
	if m[1] != "" {
		return m[1]
	}
	return m[2]
}
func payloadString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}
func setField(h *priceHistory, key, value string, e evidence) {
	if value == "" {
		return
	}
	old, ok := h.Fields[key]
	if !ok || old.UnixMilli < e.UnixMilli {
		h.Fields[key] = fieldEvidence{value, e}
	} else if old.UnixMilli == e.UnixMilli && old.Value != value {
		h.MissingEvidence = appendUnique(h.MissingEvidence, "conflicting_field:"+key)
	}
}
func appendUnique(a []string, s string) []string {
	for _, v := range a {
		if v == s {
			return a
		}
	}
	return append(a, s)
}
func parseSnapshot(r evidenceRow, id string) (*moneyEvent, map[string]any, error) {
	p := decodeObject(r.Row["content"])
	raw := str(p["SaveOrderSnapshot"])
	if raw == "" {
		return nil, nil, nil
	}
	m := snapshotIDPattern.FindStringSubmatch(raw)
	if len(m) == 0 || m[1] != id {
		return nil, nil, nil
	}
	q := snapshotPattern.FindStringSubmatch(raw)
	if len(q) == 0 {
		return nil, nil, fmt.Errorf("malformed_snapshot")
	}
	var s string
	if json.Unmarshal([]byte(q[1]), &s) != nil {
		return nil, nil, fmt.Errorf("malformed_snapshot")
	}
	order := objectValue(decodeObject(s)["order"])
	if str(order["thirdOrderId"]) != id {
		return nil, nil, fmt.Errorf("snapshot_identifier_mismatch")
	}
	g, gok := intValue(order["productTotalFee"])
	f, fok := intValue(objectValue(order["shippingAmount"])["amount"])
	a, aok := intValue(objectValue(order["otherAmount"])["amount"])
	total, tok := intValue(objectValue(order["amount"])["amount"])
	currency := str(objectValue(order["amount"])["currencyCode"])
	// Conservative bounds keep addition exact and avoid overflow from malformed logs.
	valid := func(n int64) bool { return n >= -900000000000000 && n <= 900000000000000 }
	if !gok || !fok || !aok || !tok || !valid(g) || !valid(f) || !valid(a) || !valid(total) || g+f+a != total || currency == "" {
		return nil, nil, fmt.Errorf("invalid_snapshot_amounts_or_currency")
	}
	e := &moneyEvent{evidence: rowEvidence(r), Kind: "saved_snapshot", Goods: g, Freight: f, Adjustment: a, Total: total, Currency: currency, Status: str(order["status"])}
	return e, order, nil
}
func buildPriceHistory(id string, rows []evidenceRow, complete bool) priceHistory {
	h := priceHistory{OrderSN: id, AllPagesFetched: complete, Changes: []moneyEvent{}, Rules: []ruleEvent{}, Updates: []priceUpdate{}, MissingEvidence: []string{}, Fields: map[string]fieldEvidence{}, Coverage: []coverage{}}
	states := []moneyEvent{}
	seen := map[string]bool{}
	for _, r := range rows {
		key := r.Project + "|" + r.Row["__time__"] + "|" + r.Row["content"]
		if seen[key] {
			continue
		}
		seen[key] = true
		p := decodeObject(r.Row["content"])
		if p == nil {
			h.MissingEvidence = appendUnique(h.MissingEvidence, "unparseable_log_content")
			continue
		}
		e := rowEvidence(r)
		h.Updates = append(h.Updates, extractPriceUpdates(p, id, e)...)
		s, order, err := parseSnapshot(r, id)
		if err != nil {
			h.MissingEvidence = appendUnique(h.MissingEvidence, err.Error())
		}
		if s != nil {
			if e.UnixMilli <= 0 {
				h.MissingEvidence = appendUnique(h.MissingEvidence, "missing_event_timestamp")
				continue
			}
			states = append(states, *s)
			for key, source := range map[string]string{"dsers_order_id": "dsersOrderId", "status": "status", "order_name": "orderName", "agency_id": "agencyId", "dsers_user_id": "dsersUserId", "store_id": "storeId", "created_at": "createdTime", "seller_order_id": "outerOrderId"} {
				setField(&h, key, str(order[source]), e)
			}
		}
		if str(p["msg"]) == "executeTaskSuccess" && str(p["target_id"]) == id {
			for _, v := range arrayValue(p["apply_info"]) {
				a := objectValue(v)
				h.Rules = append(h.Rules, ruleEvent{evidence: e, RuleID: str(a["rule_id"]), ActionType: str(a["action_type"]), ActionValue: sanitizeValue(a["action_value"]), Before: intPointer(a["amount_before"]), After: intPointer(a["amount_after"]), SavedMatch: "not_verified"})
			}
			d := objectValue(p["event_data"])
			setField(&h, "store_id", str(d["storeId"]), e)
			// A mapping list can refer to multiple customers; only a unique mapping is attributable.
			mappings := arrayValue(d["mappings"])
			if len(mappings) == 1 {
				m := objectValue(mappings[0])
				setField(&h, "customer_name", str(m["customerName"]), e)
			}
		}
	}
	sort.SliceStable(states, func(i, j int) bool { return states[i].UnixMilli < states[j].UnixMilli })
	h.SnapshotCount = len(states)
	for i, s := range states {
		if i > 0 && s.UnixMilli == states[i-1].UnixMilli && !sameMoney(s, states[i-1]) {
			h.MissingEvidence = appendUnique(h.MissingEvidence, "conflicting_snapshot_at_same_time")
		}
		if i == 0 || !sameMoney(s, states[i-1]) {
			h.Changes = append(h.Changes, s)
		}
	}
	if len(states) > 0 {
		h.FirstSaved = &states[0]
		h.Latest = &states[len(states)-1]
	}
	h.InitialQuote = findInitialQuote(&h, rows)
	sort.SliceStable(h.Rules, func(i, j int) bool { return h.Rules[i].UnixMilli < h.Rules[j].UnixMilli })
	for i := range h.Rules {
		rule := &h.Rules[i]
		if rule.After == nil {
			continue
		}
		for _, s := range states {
			if s.UnixMilli >= rule.UnixMilli {
				if s.Total == *rule.After {
					rule.SavedMatch = "matches_next_observed_snapshot"
				} else {
					rule.SavedMatch = "differs_from_next_observed_snapshot"
				}
				break
			}
		}
	}
	sort.SliceStable(h.Updates, func(i, j int) bool { return h.Updates[i].UnixMilli < h.Updates[j].UnixMilli })
	classifyPriceHistory(&h)
	return h
}
func sameMoney(a, b moneyEvent) bool {
	return a.Goods == b.Goods && a.Freight == b.Freight && a.Adjustment == b.Adjustment && a.Total == b.Total && a.Currency == b.Currency
}
func classifyPriceHistory(h *priceHistory) {
	h.Classification = "insufficient_evidence"
	h.HistoricalMatch = false
	h.FirstSaveDiffers = false
	h.PostSaveChanges = 0
	if len(h.Changes) > 0 {
		h.PostSaveChanges = len(h.Changes) - 1
	}
	h.RuleEvidence = "not_found_in_scope"
	if len(h.Rules) > 0 {
		h.RuleEvidence = "observed"
	}
	if !h.AllPagesFetched {
		h.RuleEvidence = "incomplete_search"
		h.MissingEvidence = appendUnique(h.MissingEvidence, "incomplete_log_coverage")
		return
	}
	if h.InitialQuote == nil {
		h.MissingEvidence = appendUnique(h.MissingEvidence, "initial_quote_not_uniquely_verified")
		return
	}
	if h.FirstSaved == nil || h.Latest == nil {
		h.MissingEvidence = appendUnique(h.MissingEvidence, "saved_snapshot_not_found")
		return
	}
	h.FirstSaveDiffers = !sameMoney(*h.InitialQuote, *h.FirstSaved)
	if h.FirstSaved.UnixMilli < h.InitialQuote.UnixMilli {
		h.MissingEvidence = appendUnique(h.MissingEvidence, "quote_after_first_snapshot")
		return
	}
	for _, s := range h.Changes {
		if s.Currency != h.InitialQuote.Currency {
			h.MissingEvidence = appendUnique(h.MissingEvidence, "mixed_or_unknown_currency")
			return
		}
	}
	for _, m := range h.MissingEvidence {
		if m != "initial_quote_not_uniquely_verified" {
			return
		}
	}
	match := func(s moneyEvent) bool { return s.Freight < h.InitialQuote.Freight && s.Total < h.InitialQuote.Total }
	for _, s := range h.Changes {
		h.HistoricalMatch = h.HistoricalMatch || match(s)
	}
	if match(*h.Latest) {
		h.Classification = "matches"
	} else if h.HistoricalMatch {
		h.Classification = "historical_match_recovered"
	} else {
		h.Classification = "not_observed"
	}
}
func itemSignature(items [][3]string) string {
	counts := map[string]int64{}
	for _, i := range items {
		q, e := strconv.ParseInt(i[2], 10, 64)
		if e != nil || q <= 0 || q > 1000000000 {
			return ""
		}
		b, _ := json.Marshal(i[:2])
		counts[string(b)] += q
	}
	if len(counts) == 0 {
		return ""
	}
	b, _ := json.Marshal(counts)
	return string(b)
}
func findInitialQuote(h *priceHistory, rows []evidenceRow) *moneyEvent {
	type creation struct {
		row evidenceRow
		p   map[string]any
		d   map[string]any
	}
	creates := []creation{}
	seen := map[string]bool{}
	for _, r := range rows {
		p := decodeObject(r.Row["content"])
		if str(p["CreateOrder"]) != "返回" {
			continue
		}
		d := objectValue(p["result"])
		for _, v := range arrayValue(d["order_list"]) {
			if str(objectValue(v)["order_id"]) == h.OrderSN {
				key := str(p["trace_id"]) + "|" + str(p["span_id"])
				if !seen[key] {
					creates = append(creates, creation{r, p, d})
					seen[key] = true
				}
			}
		}
	}
	if len(creates) != 1 {
		return nil
	}
	c := creates[0]
	e := rowEvidence(c.row)
	setField(h, "dsers_order_id", str(c.d["dsers_order_id"]), e)
	setField(h, "creation_log_time", e.Time, e)
	trace, span := str(c.p["trace_id"]), str(c.p["span_id"])
	if trace == "" || span == "" {
		return nil
	}
	argsSet := map[string]bool{}
	for _, r := range rows {
		p := decodeObject(r.Row["content"])
		if !strings.HasSuffix(str(p["operation"]), "/CreateOrder") {
			continue
		}
		reply := payloadString(p["reply"])
		requestArgs := payloadString(p["args"])
		identityBound := str(p["trace_id"]) == trace || (str(c.d["dsers_order_id"]) != "" && protoField(requestArgs, "dsers_order_id") == str(c.d["dsers_order_id"]))
		if identityBound && protoField(reply, "order_id") == h.OrderSN {
			argsSet[payloadString(p["args"])] = true
		}
	}
	if len(argsSet) != 1 {
		return nil
	}
	args := ""
	for s := range argsSet {
		args = s
	}
	for key, source := range map[string]string{"dsers_user_id": "dsers_user_id", "order_name": "dsers_order_name", "shop_name": "shopify_shop_name", "store_id": "dsers_store_id", "agency_id": "agency_id"} {
		setField(h, key, protoField(args, source), e)
	}
	items := [][3]string{}
	for _, m := range itemPattern.FindAllStringSubmatch(args, -1) {
		items = append(items, [3]string{m[1], m[2], m[3]})
	}
	sig := itemSignature(items)
	country, zip := protoField(args, "country"), protoField(args, "zip")
	if sig == "" || country == "" {
		return nil
	}
	candidates := []moneyEvent{}
	quoteSeen := map[string]bool{}
	for _, r := range rows {
		p := decodeObject(r.Row["content"])
		if str(p["Orders"]) != "获取物流包裹参数" || str(p["trace_id"]) != trace || str(p["span_id"]) != span {
			continue
		}
		req := objectValue(p["req"])
		if str(req["ship_country"]) != country || str(req["zip"]) != zip {
			continue
		}
		qi := [][3]string{}
		for _, v := range arrayValue(req["goods_items"]) {
			m := objectValue(v)
			qi = append(qi, [3]string{str(m["virtually_product_id"]), str(m["virtually_variant_id"]), str(m["count"])})
		}
		if itemSignature(qi) != sig {
			continue
		}
		result := objectValue(p["result"])
		agencies := objectValue(result["agency_goods_logistics_quotation"])
		if len(agencies) != 1 {
			continue
		}
		agency := ""
		var parcels []any
		for id, v := range agencies {
			agency = id
			parcels = arrayValue(objectValue(v)["goods_items"])
		}
		if known := h.Fields["agency_id"].Value; known != "" && known != agency {
			continue
		}
		goodsRows := arrayValue(result["goods_quotation"])
		if len(goodsRows) == 0 || len(parcels) == 0 {
			continue
		}
		goods, freight := int64(0), int64(0)
		valid := true
		for _, v := range goodsRows {
			m := objectValue(v)
			n, ok := intValue(m["goods_amount"])
			if !ok || n < 0 || n > 900000000000000 || str(m["reason"]) != "" {
				valid = false
				break
			}
			goods += n
		}
		for _, v := range parcels {
			n, ok := intValue(objectValue(v)["logistics_amount"])
			if !ok || n < 0 || n > 900000000000000 {
				valid = false
				break
			}
			freight += n
		}
		// This historical quotation schema reports USD minor units. Other currencies need their own adapter.
		if !valid || goods > 900000000000000 || freight > 900000000000000 {
			h.MissingEvidence = appendUnique(h.MissingEvidence, "quotation_amount_missing_or_invalid")
			continue
		}
		if h.FirstSaved == nil || h.FirstSaved.Currency != "USD" {
			continue
		}
		event := moneyEvent{evidence: rowEvidence(r), Kind: "creation_quote", Goods: goods, Freight: freight, Total: goods + freight, Currency: "USD"}
		b, _ := json.Marshal(event)
		if !quoteSeen[string(b)] {
			candidates = append(candidates, event)
			quoteSeen[string(b)] = true
		}
		setField(h, "agency_id", agency, e)
	}
	if len(candidates) != 1 {
		return nil
	}
	return &candidates[0]
}
