package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type queryFunc func(context.Context, slsQueryArgs) (*slsQueryResult, error)
type collectArgs struct {
	SLS         slsQueryArgs
	MaxRequests int
	MaxRows     int
}
type coverage struct {
	Project  string `json:"project"`
	Logstore string `json:"logstore"`
	From     int64  `json:"from_time"`
	To       int64  `json:"to_time"`
}
type collection struct {
	QueryCompleted  bool                `json:"query_completed"`
	AllPagesFetched bool                `json:"all_pages_fetched"`
	Coverage        coverage            `json:"coverage"`
	Requests        int                 `json:"requests"`
	Rows            []map[string]string `json:"rows"`
	MissingEvidence []string            `json:"missing_evidence"`
}

// collectAll uses half-open time windows, so splitting never overlaps boundary events.
// It does not deduplicate identical log lines: separate occurrences may be meaningful.
func collectAll(ctx context.Context, fetch queryFunc, options collectArgs) (collection, error) {
	a := options.SLS
	r := collection{Rows: []map[string]string{}, MissingEvidence: []string{}}
	if strings.TrimSpace(a.Query) == "" {
		return r, errors.New("query is required")
	}
	mode, err := resolveQueryMode(a.Mode, a.Query)
	if err != nil || mode == "analysis" || strings.Contains(a.Query, "|") {
		return r, errors.New("complete collection accepts raw searches only; use sls_query for SQL/SPL")
	}
	if a.FromTime == 0 && a.ToTime == 0 {
		a.ToTime = time.Now().Unix()
		minutes := a.Minutes
		if minutes <= 0 {
			minutes = 60
		}
		a.FromTime = a.ToTime - minutes*60
	}
	if a.FromTime <= 0 || a.ToTime <= a.FromTime {
		return r, errors.New("from_time and to_time must form a positive increasing window")
	}
	if options.MaxRequests == 0 {
		options.MaxRequests = 200
	}
	if options.MaxRows == 0 {
		options.MaxRows = 20000
	}
	if options.MaxRequests < 1 || options.MaxRequests > 2000 || options.MaxRows < 1 || options.MaxRows > 200000 {
		return r, errors.New("max_requests must be 1..2000; max_rows must be 1..200000")
	}
	a.Line = 100
	a.Offset = 0
	a.Reverse = false
	a.MaxContentChars = 0
	a.Mode = "raw"
	r.Coverage = coverage{Project: resolveProject(a.ProjectAlias, a.Project, projectDSers), Logstore: a.Logstore, From: a.FromTime, To: a.ToTime}
	if r.Coverage.Logstore == "" {
		r.Coverage.Logstore = defaultLogstore
	}
	var scan func(int64, int64) bool
	scan = func(from, to int64) bool {
		start := len(r.Rows)
		for offset := int64(0); ; offset += 100 {
			if ctx.Err() != nil {
				r.MissingEvidence = append(r.MissingEvidence, "query_cancelled")
				return false
			}
			var page *slsQueryResult
			var queryErr error
			for attempt := 0; attempt < 3; attempt++ {
				if r.Requests >= options.MaxRequests {
					r.MissingEvidence = append(r.MissingEvidence, "request_budget_exhausted")
					return false
				}
				q := a
				q.FromTime = from
				q.ToTime = to
				q.Offset = offset
				r.Requests++
				page, queryErr = fetch(ctx, q)
				if queryErr == nil && page != nil && page.Completed {
					break
				}
				if attempt < 2 {
					select {
					case <-ctx.Done():
						r.MissingEvidence = append(r.MissingEvidence, "query_cancelled")
						return false
					case <-time.After(20 * time.Millisecond):
					}
				}
			}
			if queryErr != nil || page == nil {
				r.MissingEvidence = append(r.MissingEvidence, fmt.Sprintf("query_failed_in_window:%d:%d", from, to))
				return false
			}
			if !page.Completed {
				r.Rows = r.Rows[:start]
				if to-from <= 1 {
					r.MissingEvidence = append(r.MissingEvidence, "incomplete_minimum_time_window")
					return false
				}
				mid := from + (to-from)/2
				left := scan(from, mid)
				right := scan(mid, to)
				return left && right
			}
			remaining := options.MaxRows - len(r.Rows)
			if len(page.Rows) > remaining {
				r.Rows = append(r.Rows, page.Rows[:remaining]...)
				r.MissingEvidence = append(r.MissingEvidence, "row_budget_exhausted")
				return false
			}
			r.Rows = append(r.Rows, page.Rows...)
			if len(page.Rows) < 100 {
				return true
			}
		}
	}
	r.AllPagesFetched = scan(a.FromTime, a.ToTime)
	r.QueryCompleted = r.AllPagesFetched
	return r, nil
}

// UseNumber preserves 64-bit identifiers while walking nested JSON strings.
func decodeValue(s string) any {
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	var v any
	if d.Decode(&v) != nil {
		return nil
	}
	return v
}
func decodeObject(s string) map[string]any { m, _ := decodeValue(s).(map[string]any); return m }
func objectValue(v any) map[string]any {
	if s, ok := v.(string); ok {
		return decodeObject(s)
	}
	m, _ := v.(map[string]any)
	return m
}
func arrayValue(v any) []any {
	if s, ok := v.(string); ok {
		v = decodeValue(s)
	}
	a, _ := v.([]any)
	return a
}
func str(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case json.Number:
		return string(v)
	}
	return ""
}
func walkObjects(v any, depth int, visit func(map[string]any)) {
	if depth > 20 {
		return
	}
	switch x := v.(type) {
	case map[string]any:
		visit(x)
		for _, child := range x {
			walkObjects(child, depth+1, visit)
		}
	case []any:
		for _, child := range x {
			walkObjects(child, depth+1, visit)
		}
	case string:
		if len(x) > 0 && (x[0] == '{' || x[0] == '[') {
			if d := decodeValue(x); d != nil {
				walkObjects(d, depth+1, visit)
			}
		}
	}
}

var secretKey = regexp.MustCompile(`(?i)(token|secret|password|passwd|cookie|authorization|credential|access.?key|headers)`)
var secretText = regexp.MustCompile(`(?i)(bearer\s+[^\s",}]+|(?:token|password|secret|cookie|authorization|access[_-]?key)\s*[:=]\s*"?[^\s",}]+)`)

func sanitizeValue(v any) any { return sanitizeDepth(v, 0) }
func sanitizeDepth(v any, depth int) any {
	if depth > 20 {
		return "<depth-limit>"
	}
	switch x := v.(type) {
	case map[string]any:
		m := map[string]any{}
		for k, v := range x {
			if !secretKey.MatchString(k) {
				m[k] = sanitizeDepth(v, depth+1)
			}
		}
		return m
	case []any:
		a := make([]any, len(x))
		for i, v := range x {
			a[i] = sanitizeDepth(v, depth+1)
		}
		return a
	case string:
		if d := decodeValue(x); d != nil {
			switch d.(type) {
			case map[string]any, []any:
				return sanitizeDepth(d, depth+1)
			}
		}
		return secretText.ReplaceAllString(x, "<redacted>")
	default:
		return v
	}
}
