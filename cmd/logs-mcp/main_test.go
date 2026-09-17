package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseSLSKeyResponseAcceptsWrappedData(t *testing.T) {
	body := []byte(`{
		"code": 0,
		"data": {
			"endpoint": "cn-test.log.aliyuncs.com",
			"accessKeyId": "test-ak",
			"accessKeySecret": "test-sk",
			"securityToken": "test-token",
			"project": "test-project",
			"logstore": "test-logstore"
		}
	}`)

	cfg, err := parseSLSKeyResponse(body)
	if err != nil {
		t.Fatalf("parseSLSKeyResponse returned error: %v", err)
	}
	if cfg.Endpoint != "cn-test.log.aliyuncs.com" {
		t.Fatalf("endpoint = %q", cfg.Endpoint)
	}
	if cfg.AccessKeyID != "test-ak" {
		t.Fatalf("access key id = %q", cfg.AccessKeyID)
	}
	if cfg.AccessKeySecret != "test-sk" {
		t.Fatalf("access key secret = %q", cfg.AccessKeySecret)
	}
	if cfg.SecurityToken != "test-token" {
		t.Fatalf("security token = %q", cfg.SecurityToken)
	}
	if cfg.Project != "test-project" {
		t.Fatalf("project = %q", cfg.Project)
	}
	if cfg.Logstore != "test-logstore" {
		t.Fatalf("logstore = %q", cfg.Logstore)
	}
}

func TestGetSLSKeyConfigCachesFetchedConfigInMemory(t *testing.T) {
	var calls int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {
				"ALIYUN_SLS_ENDPOINT": "cn-test.log.aliyuncs.com",
				"ALIBABA_CLOUD_ACCESS_KEY_ID": "test-ak",
				"ALIBABA_CLOUD_ACCESS_KEY_SECRET": "test-sk"
			}
		}`))
	}))
	defer api.Close()

	server := &mcpServer{
		keyURL:      api.URL,
		keyCacheTTL: time.Hour,
		httpClient:  api.Client(),
	}

	first, err := server.getSLSKeyConfig(context.Background())
	if err != nil {
		t.Fatalf("first getSLSKeyConfig returned error: %v", err)
	}
	second, err := server.getSLSKeyConfig(context.Background())
	if err != nil {
		t.Fatalf("second getSLSKeyConfig returned error: %v", err)
	}
	if first != second {
		t.Fatal("expected cached config pointer to be reused")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("endpoint calls = %d, want 1", got)
	}
}

func TestLiveKeyEndpointShape(t *testing.T) {
	url := os.Getenv("LIVE_KEY_ENDPOINT")
	if url == "" {
		t.Skip("set LIVE_KEY_ENDPOINT to verify a real key endpoint")
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", serverName+"/"+serverVersion+" test")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		t.Fatalf("HTTP status = %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if _, err := parseSLSKeyResponse(body); err != nil {
		t.Fatalf("parse response shape failed: %v; body_bytes=%d", err, len(body))
	}
}

func TestResolveQueryModeKeepsNumericOrderIDsAsRawQueries(t *testing.T) {
	tests := []struct {
		name  string
		mode  string
		query string
		want  string
	}{
		{name: "order id", query: `"2078691992856297600"`, want: "raw"},
		{name: "hex trace", query: `"c587193e8fe7e0618e6961f010038bf9"`, want: "trace"},
		{name: "analysis", query: `* | SELECT count(*) AS total`, want: "analysis"},
		{name: "forced raw", mode: "raw", query: `"c587193e8fe7e0618e6961f010038bf9"`, want: "raw"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveQueryMode(test.mode, test.query)
			if err != nil {
				t.Fatalf("resolveQueryMode returned error: %v", err)
			}
			if got != test.want {
				t.Fatalf("mode = %q, want %q", got, test.want)
			}
		})
	}
}

func TestPrepareQueryAddsLimitOnlyForAnalysis(t *testing.T) {
	query, receipt := prepareQuery(slsQueryArgs{Query: `* | SELECT count(*) AS total`, Offset: 5, Reverse: true}, "analysis", 1, 2, 100)
	if query != `* | SELECT count(*) AS total LIMIT 100` {
		t.Fatalf("query = %q", query)
	}
	if receipt.EffectiveOffset != 0 || receipt.EffectiveReverse {
		t.Fatalf("analysis receipt should ignore offset/reverse: %#v", receipt)
	}

	query, receipt = prepareQuery(slsQueryArgs{Query: `*`, Minutes: 2880}, "raw", 1, 1+48*60*60, 100)
	if query != "*" {
		t.Fatalf("raw query changed to %q", query)
	}
	if len(receipt.Warnings) == 0 {
		t.Fatal("expected full scan warning")
	}
}

func TestParseTraceTimelineEventUsesBeijingTimestampAndMessageLimit(t *testing.T) {
	row := map[string]string{
		"_time_":  "1784646731295",
		"content": `{"service.name":"merchant-order-core","operation":"CreatePBLTry","level":"error","code":500,"message":"redislock: not obtained"}`,
	}
	event := parseTraceTimelineEvent(row, projectDSers, 12)
	if event.UnixMilli != 1784646731295 {
		t.Fatalf("unix millis = %d", event.UnixMilli)
	}
	if event.Service != "merchant-order-core" || event.Operation != "CreatePBLTry" {
		t.Fatalf("unexpected parsed event: %#v", event)
	}
	if event.Message != "redislock: n...<truncated>" {
		t.Fatalf("message = %q", event.Message)
	}
	if event.Time == "" {
		t.Fatal("expected formatted timestamp")
	}
}
