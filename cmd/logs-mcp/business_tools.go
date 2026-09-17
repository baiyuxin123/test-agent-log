package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type emailEvidence struct {
	Email      string `json:"email"`
	IDField    string `json:"id_field"`
	EmailField string `json:"email_field"`
	evidence
}
type emailResolution struct {
	UserID   string          `json:"user_id"`
	Email    string          `json:"email,omitempty"`
	Status   string          `json:"status"`
	Evidence []emailEvidence `json:"evidence"`
}

func resolveEmailsFromRows(ids []string, rows []evidenceRow, complete bool) []emailResolution {
	wanted := map[string]bool{}
	found := map[string][]emailEvidence{}
	for _, id := range ids {
		wanted[id] = true
	}
	for _, row := range rows {
		e := rowEvidence(row)
		walkObjects(decodeObject(row.Row["content"]), 0, func(m map[string]any) {
			for _, ik := range []string{"id", "userId", "customerId", "dsersUserId"} {
				id := str(m[ik])
				if !wanted[id] {
					continue
				}
				for _, ek := range []string{"email", "customerEmail", "userEmail"} {
					email := strings.TrimSpace(str(m[ek]))
					addr, err := mail.ParseAddress(email)
					if err != nil || addr.Address != email || !strings.Contains(email, "@") {
						continue
					}
					found[id] = append(found[id], emailEvidence{email, ik, ek, e})
				}
			}
		})
	}
	results := make([]emailResolution, len(ids))
	for i, id := range ids {
		values := map[string]bool{}
		ev := []emailEvidence{}
		seen := map[string]bool{}
		for _, e := range found[id] {
			values[e.Email] = true
			b, _ := json.Marshal(e)
			if !seen[string(b)] {
				ev = append(ev, e)
				seen[string(b)] = true
			}
		}
		sort.SliceStable(ev, func(i, j int) bool {
			if ev[i].UnixMilli != ev[j].UnixMilli {
				return ev[i].UnixMilli < ev[j].UnixMilli
			}
			return ev[i].Email < ev[j].Email
		})
		r := emailResolution{UserID: id, Status: "not_found_in_scope", Evidence: ev}
		if len(values) > 1 {
			r.Status = "conflict"
		} else if !complete {
			r.Status = "incomplete_search"
		} else if len(values) == 1 {
			r.Status = "resolved"
			for v := range values {
				r.Email = v
			}
		}
		results[i] = r
	}
	return results
}

type businessArgs struct {
	EnrichContext bool     `json:"-"`
	OrderSN       string   `json:"order_sn"`
	OrderSNs      []string `json:"order_sns"`
	UserIDs       []string `json:"user_ids"`
	Fields        []string `json:"fields"`
	FromTime      int64    `json:"from_time"`
	ToTime        int64    `json:"to_time"`
	Days          int64    `json:"days"`
	Concurrency   int      `json:"concurrency"`
	MaxRequests   int      `json:"max_requests"`
	MaxRows       int      `json:"max_rows"`
}

var numericID = regexp.MustCompile(`^[0-9]{1,32}$`)

func (a *businessArgs) validate(name string) error {
	ids := a.OrderSNs
	if name == "order_price_history" {
		ids = []string{a.OrderSN}
	}
	if name == "resolve_user_emails" {
		ids = a.UserIDs
	}
	if len(ids) == 0 || len(ids) > 1000 {
		return errors.New("supply 1..1000 string identifiers")
	}
	for _, id := range ids {
		if !numericID.MatchString(id) {
			return errors.New("identifiers must be digit strings, never JSON numbers")
		}
	}
	if a.Concurrency == 0 {
		a.Concurrency = 5
	}
	if a.Concurrency < 1 || a.Concurrency > 10 {
		return errors.New("concurrency must be 1..10")
	}
	if a.MaxRequests == 0 {
		a.MaxRequests = 200
	}
	if a.MaxRequests < 1 || a.MaxRequests > 2000 {
		return errors.New("max_requests must be 1..2000 per order or email group")
	}
	if a.MaxRows == 0 {
		a.MaxRows = 20000
	}
	if a.MaxRows < 1 || a.MaxRows > 200000 {
		return errors.New("max_rows must be 1..200000 per order or email group")
	}
	if a.FromTime == 0 && a.ToTime == 0 {
		days := a.Days
		if days == 0 {
			days = 30
			if name == "resolve_user_emails" {
				days = 180
			}
		}
		if days < 1 || days > 366 {
			return errors.New("days must be 1..366")
		}
		a.ToTime = time.Now().Unix()
		a.FromTime = a.ToTime - days*86400
	}
	if a.FromTime <= 0 || a.ToTime <= a.FromTime {
		return errors.New("from_time and to_time must both be supplied and increasing")
	}
	for _, f := range a.Fields {
		if !contextFields[f] {
			return fmt.Errorf("unsupported context field: %s", f)
		}
	}
	return nil
}

var contextFields = map[string]bool{"order_name": true, "dsers_order_id": true, "status": true, "agency_id": true, "dsers_user_id": true, "shop_name": true, "store_id": true, "customer_name": true, "created_at": true, "creation_log_time": true, "seller_order_id": true}

type investigation struct {
	fetch    queryFunc
	args     businessArgs
	rows     []evidenceRow
	coverage []coverage
	missing  []string
	requests int
	complete bool
}

func (i *investigation) query(ctx context.Context, project, q string) {
	if ctx.Err() != nil {
		i.complete = false
		i.missing = appendUnique(i.missing, "query_cancelled")
		return
	}
	left := i.args.MaxRequests - i.requests
	room := i.args.MaxRows - len(i.rows)
	if left <= 0 || room <= 0 {
		i.complete = false
		i.missing = appendUnique(i.missing, "investigation_budget_exhausted")
		return
	}
	queryArgs := slsQueryArgs{ProjectAlias: project, Query: q, FromTime: i.args.FromTime, ToTime: i.args.ToTime}
	if strings.HasPrefix(project, "k8s-log-") {
		queryArgs.Project = project
		queryArgs.ProjectAlias = ""
	}
	r, err := collectAll(ctx, i.fetch, collectArgs{SLS: queryArgs, MaxRequests: left, MaxRows: room})
	if err != nil {
		i.complete = false
		i.missing = appendUnique(i.missing, "invalid_or_failed_query")
		return
	}
	i.requests += r.Requests
	i.complete = i.complete && r.AllPagesFetched
	i.coverage = append(i.coverage, r.Coverage)
	i.missing = append(i.missing, r.MissingEvidence...)
	for _, row := range r.Rows {
		i.rows = append(i.rows, evidenceRow{r.Coverage.Project, row})
	}
}
func investigateOrder(ctx context.Context, fetch queryFunc, a businessArgs, id string) priceHistory {
	i := investigation{fetch: fetch, args: a, complete: true}
	for _, project := range []string{"dsers", "dianshi"} {
		i.query(ctx, project, `"`+id+`" and (CreateOrder or (SaveOrderSnapshot and ORDER_CHANGE) or executeTaskSuccess or BatchUpdateOrderPrice)`)
	}
	// Retrieve the creation quote, which often contains only a trace ID rather than an order ID.
	traces := map[string]string{}
	for _, r := range i.rows {
		p := decodeObject(r.Row["content"])
		if str(p["CreateOrder"]) != "返回" {
			continue
		}
		for _, v := range arrayValue(objectValue(p["result"])["order_list"]) {
			if str(objectValue(v)["order_id"]) == id {
				trace := str(p["trace_id"])
				if validTrace(trace) {
					traces[r.Project+"|"+trace] = trace
				}
			}
		}
	}
	keys := make([]string, 0, len(traces))
	for key := range traces {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for n, key := range keys {
		if n >= 20 {
			i.complete = false
			i.missing = appendUnique(i.missing, "too_many_creation_traces")
			break
		}
		project := strings.SplitN(key, "|", 2)[0]
		i.query(ctx, project, `"`+traces[key]+`" and (CreateOrder or "获取物流包裹参数")`)
	}
	h := buildPriceHistory(id, i.rows, i.complete)
	// Resolve customer name only from an object with the exact agency/user pair.
	agency, user := h.Fields["agency_id"].Value, h.Fields["dsers_user_id"].Value
	if a.EnrichContext && numericID.MatchString(agency) && numericID.MatchString(user) {
		before := len(i.rows)
		for _, project := range []string{"dsers", "dianshi"} {
			i.query(ctx, project, `"`+agency+`" and "`+user+`" and (customerName or customer_name) and (custom_id or customId)`)
		}
		for _, row := range i.rows[before:] {
			walkObjects(decodeObject(row.Row["content"]), 0, func(m map[string]any) {
				aid := firstString(m, "agencyId", "agency_id")
				uid := firstString(m, "customId", "custom_id")
				if aid == agency && uid == user {
					setField(&h, "customer_name", firstString(m, "customerName", "customer_name"), rowEvidence(row))
				}
			})
		}
	}
	h.Coverage = i.coverage
	h.AllPagesFetched = i.complete
	h.MissingEvidence = append(h.MissingEvidence, i.missing...)
	classifyPriceHistory(&h)
	return h
}
func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v := str(m[k]); v != "" {
			return v
		}
	}
	return ""
}

var safeTrace = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

func validTrace(s string) bool { return safeTrace.MatchString(s) }
func runOrderBatch(ctx context.Context, fetch queryFunc, a businessArgs) []priceHistory {
	unique := []string{}
	indexes := map[string]int{}
	for _, id := range a.OrderSNs {
		if _, ok := indexes[id]; !ok {
			indexes[id] = len(unique)
			unique = append(unique, id)
		}
	}
	records := make([]priceHistory, len(unique))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < a.Concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				records[index] = investigateOrder(ctx, fetch, a, unique[index])
			}
		}()
	}
	for n := range unique {
		jobs <- n
	}
	close(jobs)
	wg.Wait()
	out := make([]priceHistory, len(a.OrderSNs))
	for n, id := range a.OrderSNs {
		out[n] = records[indexes[id]]
	}
	return out
}
func runEmailBatch(ctx context.Context, fetch queryFunc, a businessArgs) map[string]any {
	unique := []string{}
	seen := map[string]bool{}
	for _, id := range a.UserIDs {
		if !seen[id] {
			unique = append(unique, id)
			seen[id] = true
		}
	}
	type groupResult struct {
		results  []emailResolution
		coverage []coverage
		missing  []string
		complete bool
	}
	groups := (len(unique) + 19) / 20
	out := make([]groupResult, groups)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for n := 0; n < a.Concurrency; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for g := range jobs {
				end := (g + 1) * 20
				if end > len(unique) {
					end = len(unique)
				}
				ids := unique[g*20 : end]
				quoted := []string{}
				for _, id := range ids {
					quoted = append(quoted, `"`+id+`"`)
				}
				i := investigation{fetch: fetch, args: a, complete: true}
				for _, project := range []string{"dsers", "dianshi"} {
					i.query(ctx, project, "("+strings.Join(quoted, " or ")+") and (email or customerEmail or userEmail)")
				}
				for _, r := range i.rows {
					if decodeObject(r.Row["content"]) == nil {
						i.complete = false
						i.missing = appendUnique(i.missing, "unparseable_log_content")
					}
				}
				out[g] = groupResult{resolveEmailsFromRows(ids, i.rows, i.complete), i.coverage, i.missing, i.complete}
			}
		}()
	}
	for n := 0; n < groups; n++ {
		jobs <- n
	}
	close(jobs)
	wg.Wait()
	byID := map[string]emailResolution{}
	coverage := []coverage{}
	missing := []string{}
	complete := true
	for _, g := range out {
		for _, r := range g.results {
			byID[r.UserID] = r
		}
		coverage = append(coverage, g.coverage...)
		missing = append(missing, g.missing...)
		complete = complete && g.complete
	}
	results := make([]emailResolution, len(a.UserIDs))
	for n, id := range a.UserIDs {
		results[n] = byID[id]
	}
	return map[string]any{"records": results, "coverage": coverage, "all_pages_fetched": complete, "missing_evidence": missing, "input_count": len(a.UserIDs), "unique_count": len(unique)}
}

func businessToolDefinitions() []map[string]any {
	integer := func(def, min, max int) map[string]any {
		return map[string]any{"type": "integer", "default": def, "minimum": min, "maximum": max}
	}
	tools := []map[string]any{}
	for _, name := range []string{"sls_query_all", "order_price_history", "order_price_audit", "order_context", "resolve_user_emails"} {
		props := map[string]any{"from_time": map[string]any{"type": "integer", "description": "Inclusive Unix seconds; supply with to_time."}, "to_time": map[string]any{"type": "integer", "description": "Exclusive Unix seconds; fixes the investigation cutoff."}, "max_requests": integer(200, 1, 2000), "max_rows": integer(20000, 1, 200000)}
		ids := map[string]any{"type": "array", "items": map[string]any{"type": "string", "pattern": "^[0-9]{1,32}$"}, "minItems": 1, "maxItems": 1000}
		required := []string{}
		description := ""
		if name == "sls_query_all" {
			props["query"] = map[string]any{"type": "string"}
			props["project_alias"] = map[string]any{"type": "string", "enum": []string{"dsers", "dianshi", "shopify", "aofei", "傲飞", "cc338", "c195"}}
			props["project"] = map[string]any{"type": "string"}
			props["logstore"] = map[string]any{"type": "string"}
			props["minutes"] = integer(60, 1, 527040)
			required = []string{"query"}
			description = "Collect all pages of a raw SLS search within explicit budgets. Retries and splits incomplete windows. Returns independent completeness and coverage metadata; never interprets one complete page as all pages. Credential fields are redacted."
		} else {
			days := 30
			if name == "resolve_user_emails" {
				days = 180
			}
			props["days"] = integer(days, 1, 366)
			props["concurrency"] = integer(5, 1, 10)
			switch name {
			case "order_price_history":
				props["order_sn"] = map[string]any{"type": "string", "pattern": "^[0-9]{1,32}$"}
				required = []string{"order_sn"}
				description = "Reconstruct a DianShi order's creation quotation, first observed saved amount, saved changes, rule evidence and latest LOG snapshot. Monetary values are minor units. Missing evidence never means no rule execution."
			case "order_price_audit":
				props["order_sns"] = ids
				required = []string{"order_sns"}
				description = "Batch audit exact DianShi order_sn strings in input order. Predicate: freight decreased AND latest saved total below creation quotation. Classifies matches, historical_match_recovered, not_observed, insufficient_evidence. This classifies a price pattern, not root cause or rule matching."
			case "order_context":
				props["order_sns"] = ids
				props["fields"] = map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"order_name", "dsers_order_id", "status", "agency_id", "dsers_user_id", "shop_name", "store_id", "customer_name", "created_at", "creation_log_time", "seller_order_id"}}}
				required = []string{"order_sns"}
				description = "Resolve order/account fields for DianShi order_sn strings, with per-field log evidence. fields selects output columns. Latest log status is not a live business-system readback. Missing fields remain explicit."
			case "resolve_user_emails":
				props["user_ids"] = ids
				required = []string{"user_ids"}
				description = "Batch resolve account emails from exact ID and email fields in the SAME JSON object, including nested JSON strings. Preserves input order and duplicates, identifies conflicts, searches both projects, default 180-day scope."
			}
		}
		tools = append(tools, map[string]any{"name": name, "description": description, "inputSchema": map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}})
	}
	return tools
}
func (s *mcpServer) callBusinessTool(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	// Validate before retrieving credentials or contacting the network.
	if name == "sls_query_all" {
		var a struct {
			slsQueryArgs
			MaxRequests int `json:"max_requests"`
			MaxRows     int `json:"max_rows"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return toolError(errors.New("invalid query arguments")), nil
		}
		// Validation runs without invoking fetch.
		validationCtx, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := collectAll(validationCtx, nil, collectArgs{a.slsQueryArgs, a.MaxRequests, a.MaxRows}); err != nil {
			return toolError(err), nil
		}
		svc, err := s.getSLS(ctx)
		if err != nil {
			return toolError(errors.New("SLS initialization failed")), nil
		}
		result, err := collectAll(ctx, svc.query, collectArgs{a.slsQueryArgs, a.MaxRequests, a.MaxRows})
		if err != nil {
			return toolError(err), nil
		}
		for _, row := range result.Rows {
			for k, v := range row {
				if secretKey.MatchString(k) {
					delete(row, k)
					continue
				}
				if k == "content" {
					if obj := decodeObject(v); obj != nil {
						b, _ := json.Marshal(sanitizeValue(obj))
						row[k] = string(b)
					} else {
						row[k] = "<unstructured content omitted>"
					}
				} else {
					row[k] = secretText.ReplaceAllString(v, "<redacted>")
				}
			}
		}
		return toolJSON(result), nil
	}
	var a businessArgs
	if json.Unmarshal(raw, &a) != nil {
		return toolError(errors.New("invalid business arguments")), nil
	}
	if err := a.validate(name); err != nil {
		return toolError(err), nil
	}
	svc, err := s.getSLS(ctx)
	if err != nil {
		return toolError(errors.New("SLS initialization failed")), nil
	}
	if name == "resolve_user_emails" {
		return toolJSON(runEmailBatch(ctx, svc.query, a)), nil
	}
	if name == "order_price_history" {
		return toolJSON(investigateOrder(ctx, svc.query, a, a.OrderSN)), nil
	}
	a.EnrichContext = name == "order_context"
	records := runOrderBatch(ctx, svc.query, a)
	if name == "order_context" {
		selected := a.Fields
		if len(selected) == 0 {
			for f := range contextFields {
				selected = append(selected, f)
			}
			sort.Strings(selected)
		}
		result := []map[string]any{}
		for _, h := range records {
			fields := map[string]fieldEvidence{}
			missing := []string{}
			for _, f := range selected {
				if v, ok := h.Fields[f]; ok {
					fields[f] = v
				} else {
					missing = append(missing, f)
				}
			}
			result = append(result, map[string]any{"order_sn": h.OrderSN, "fields": fields, "missing_fields": missing, "all_pages_fetched": h.AllPagesFetched, "coverage": h.Coverage, "evidence_warnings": h.MissingEvidence, "source": "latest_observed_logs"})
		}
		return toolJSON(map[string]any{"records": result}), nil
	}
	counts := map[string]int{}
	unique := map[string]bool{}
	complete := true
	for _, r := range records {
		if !unique[r.OrderSN] {
			counts[r.Classification]++
			unique[r.OrderSN] = true
		}
		complete = complete && r.AllPagesFetched
	}
	return toolJSON(map[string]any{"records": records, "input_count": len(records), "unique_count": len(unique), "unique_counts": counts, "all_pages_fetched": complete, "predicate": "latest.freight < initial_quote.freight AND latest.total < initial_quote.total", "cutoff": a.ToTime}), nil
}

const priceAuditRules = `# Price audit evidence contract
- All order_sn and account IDs are strings. Never convert them through floating point.
- Coverage uses [from_time,to_time) Unix seconds; latest log is not live business state.
- query_completed on sls_query_all describes successful exhaustion of its window tree. all_pages_fetched also requires exhausting pagination within the request/row budgets. Legacy sls_query.completed only describes one SLS response.
- Initial quotation, first observed persisted amount, rule before/after amounts, and latest observed saved amount are distinct stages.
- Creation quotation is attributable only with exact order response, trace/span, item quantities, country/postcode, and one unambiguous agency. The supported historical quotation adapter uses USD minor units; other currencies remain insufficient evidence.
- No missing amount is filled with zero. Saved amounts must balance: goods + freight + adjustment = total.
- Price classification requires complete scoped query coverage and verified quotation/snapshots. Missing historical evidence is not a negative signal.
- A price-pattern match is not a rule match or a root-cause diagnosis. Missing rule logs do not prove non-execution.
- A rule amount matching the next observed snapshot is correlation; it is not independent proof of causation.
- Same-time contradictory snapshots, malformed logs and ambiguous attribution remain explicit evidence gaps.
- Account emails require ID and email in the same structured object; unrelated nested shop/supplier/recipient email is not sufficient. Conflicts are returned without choosing a winner.
- Batch tools preserve input order and duplicate rows, execute each unique order once and cap concurrency at ten. Budgets apply per unique order or email group of at most twenty IDs.
- Outputs contain allowlisted business evidence. Exports, formatting and message delivery belong to the caller; these tools do not send reports.
`
