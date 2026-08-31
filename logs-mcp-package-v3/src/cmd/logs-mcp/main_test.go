package main

import (
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

	first, err := server.getSLSKeyConfig(t.Context())
	if err != nil {
		t.Fatalf("first getSLSKeyConfig returned error: %v", err)
	}
	second, err := server.getSLSKeyConfig(t.Context())
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
