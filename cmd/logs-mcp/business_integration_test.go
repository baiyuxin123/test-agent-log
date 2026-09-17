package main

import (
	"context"
	"encoding/json"
	sls "github.com/aliyun/aliyun-log-go-sdk"
	"strings"
	"testing"
	"time"
)

type fixtureSLS struct{ sls.ClientInterface }

func (f fixtureSLS) GetLogs(project, logstore, topic string, from, to int64, query string, line, offset int64, reverse bool) (*sls.GetLogsResponse, error) {
	rows := []map[string]string{}
	if project == projectDSers && offset == 0 {
		if strings.HasPrefix(query, `"123" and (`) {
			rows = append(rows, snapshot("123", "20", 100, 20, 0, 120).Row)
		}
		if strings.Contains(query, "customerEmail") || query == "fixture" {
			rows = append(rows, map[string]string{"__time__": "20", "content": `{"userId":"777","email":"account@example.com","args":"{\"token\":\"SECRET\"}"}`})
		}
	}
	return &sls.GetLogsResponse{Progress: "Complete", Count: int64(len(rows)), Logs: rows}, nil
}
func fixtureServer() *mcpServer {
	cfg := &slsKeyConfig{Project: projectDSers, Logstore: defaultLogstore}
	return &mcpServer{cachedSLSKey: cfg, cachedSLSKeyAt: time.Now(), slsKey: cfg, sls: &slsService{client: fixtureSLS{}, defaultProject: projectDSers, defaultLogstore: defaultLogstore}}
}
func TestBusinessToolsThroughMCPDispatch(t *testing.T) {
	s := fixtureServer()
	calls := []struct {
		name, args string
		verify     func(map[string]any)
	}{
		{"sls_query_all", `{"query":"fixture","from_time":1,"to_time":30}`, func(m map[string]any) {
			if m["all_pages_fetched"] != true || len(m["rows"].([]any)) != 1 {
				t.Fatal("collection contract lost")
			}
		}},
		{"order_price_history", `{"order_sn":"123","from_time":1,"to_time":30}`, func(m map[string]any) {
			if m["classification"] != "insufficient_evidence" || m["latest_log_snapshot"].(map[string]any)["total_minor"] != float64(120) {
				t.Fatal("history evidence lost")
			}
		}},
		{"order_price_audit", `{"order_sns":["123","456","123"],"from_time":1,"to_time":30,"concurrency":2}`, func(m map[string]any) {
			if m["input_count"] != float64(3) || m["unique_count"] != float64(2) {
				t.Fatal("batch order/duplicate contract lost")
			}
		}},
		{"order_context", `{"order_sns":["123"],"fields":["status"],"from_time":1,"to_time":30}`, func(m map[string]any) {
			r := m["records"].([]any)[0].(map[string]any)
			f := r["fields"].(map[string]any)
			if len(f) != 1 || f["status"].(map[string]any)["value"] != "WAIT_BUYER_PAY" {
				t.Fatal("field projection lost")
			}
		}},
		{"resolve_user_emails", `{"user_ids":["777","888","777"],"from_time":1,"to_time":30}`, func(m map[string]any) {
			r := m["records"].([]any)
			if len(r) != 3 || r[0].(map[string]any)["email"] != "account@example.com" || r[1].(map[string]any)["status"] != "not_found_in_scope" {
				t.Fatal("email contract lost")
			}
		}},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			raw := json.RawMessage(`{"name":"` + tc.name + `","arguments":` + tc.args + `}`)
			result, err := s.callTool(context.Background(), raw)
			if err != nil {
				t.Fatal(err)
			}
			m := result.(map[string]any)
			if m["isError"] != false {
				t.Fatal("MCP returned error")
			}
			text := m["content"].([]map[string]any)[0]["text"].(string)
			if strings.Contains(text, "SECRET") {
				t.Fatal("credential leaked through MCP")
			}
			var data map[string]any
			if json.Unmarshal([]byte(text), &data) != nil {
				t.Fatal("invalid tool result")
			}
			tc.verify(data)
		})
	}
	result, err := readResource(json.RawMessage(`{"uri":"logs://price-audit-rules"}`))
	if err != nil || result == nil {
		t.Fatal("resource unavailable")
	}
}
