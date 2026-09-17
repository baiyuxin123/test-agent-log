package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	sls "github.com/aliyun/aliyun-log-go-sdk"
)

const (
	serverName    = "dsers-logs-mcp"
	serverVersion = "0.3.0"

	projectDSers    = "k8s-log-cc338bd1e67fa4d8e9be4ad1e9435670a"
	projectDianShi  = "k8s-log-c19589a718db24dbb835525e4e8f2c2a0"
	defaultLogstore = "dsers-app"
	defaultKeyURL   = "https://super-app-api-gw-test.dsers.com/dsers-logistics-mgmt-bff/main/aliyun-key"
	defaultKeyTTL   = time.Hour
	defaultIndexTTL = 15 * time.Minute
)

var (
	traceIDPattern  = regexp.MustCompile(`(?i)\b[0-9a-f]{16,64}\b`)
	sqlLimitPattern = regexp.MustCompile(`(?i)\blimit\s+\d+`)
	beijingLocation = time.FixedZone("CST", 8*60*60)
)

type rpcRequest struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Result  any              `json:"result,omitempty"`
	Error   *rpcError        `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpServer struct {
	envFile          string
	keyURL           string
	keyCacheTTL      time.Duration
	allowEnvFallback bool
	httpClient       *http.Client
	cachedSLSKey     *slsKeyConfig
	cachedSLSKeyAt   time.Time
	sls              *slsService
	slsKey           *slsKeyConfig
	stdioMode        string
}

type slsService struct {
	client          sls.ClientInterface
	defaultProject  string
	defaultLogstore string
	indexCache      map[string]cachedIndex
	indexCacheTTL   time.Duration
}

type cachedIndex struct {
	value     *sls.Index
	fetchedAt time.Time
}

type slsKeyConfig struct {
	Endpoint        string
	AccessKeyID     string
	AccessKeySecret string
	SecurityToken   string
	Project         string
	Logstore        string
}

type slsQueryArgs struct {
	Project         string `json:"project,omitempty"`
	ProjectAlias    string `json:"project_alias,omitempty"`
	Logstore        string `json:"logstore,omitempty"`
	Topic           string `json:"topic,omitempty"`
	Query           string `json:"query"`
	Minutes         int64  `json:"minutes,omitempty"`
	FromTime        int64  `json:"from_time,omitempty"`
	ToTime          int64  `json:"to_time,omitempty"`
	Line            int64  `json:"line,omitempty"`
	Offset          int64  `json:"offset,omitempty"`
	Reverse         bool   `json:"reverse,omitempty"`
	MaxContentChars int    `json:"max_content_chars,omitempty"`
	Mode            string `json:"mode,omitempty"`
}

type slsQueryResult struct {
	Completed bool                `json:"completed"`
	Count     int64               `json:"count"`
	From      int64               `json:"from"`
	To        int64               `json:"to"`
	Project   string              `json:"project"`
	Logstore  string              `json:"logstore"`
	Query     string              `json:"query"`
	Rows      []map[string]string `json:"rows"`
	Receipt   queryReceipt        `json:"receipt"`
}

type queryReceipt struct {
	Mode             string   `json:"mode"`
	RequestedMode    string   `json:"requested_mode,omitempty"`
	EffectiveLine    int64    `json:"effective_line"`
	EffectiveOffset  int64    `json:"effective_offset"`
	EffectiveReverse bool     `json:"effective_reverse"`
	Warnings         []string `json:"warnings,omitempty"`
}

type slsInspectIndexArgs struct {
	Project      string `json:"project,omitempty"`
	ProjectAlias string `json:"project_alias,omitempty"`
	Logstore     string `json:"logstore,omitempty"`
	Refresh      bool   `json:"refresh,omitempty"`
}

type slsIndexField struct {
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	CaseSensitive bool     `json:"case_sensitive"`
	DocValue      bool     `json:"doc_value"`
	JSONKeys      []string `json:"json_keys,omitempty"`
}

type slsIndexResult struct {
	Project       string          `json:"project"`
	Logstore      string          `json:"logstore"`
	Cached        bool            `json:"cached"`
	FetchedAt     string          `json:"fetched_at"`
	FullText      bool            `json:"full_text"`
	FullTextToken []string        `json:"full_text_token,omitempty"`
	Fields        []slsIndexField `json:"fields"`
	QueryHints    []string        `json:"query_hints"`
}

type traceTimelineArgs struct {
	TraceID         string `json:"trace_id"`
	Project         string `json:"project,omitempty"`
	ProjectAlias    string `json:"project_alias,omitempty"`
	Logstore        string `json:"logstore,omitempty"`
	Minutes         int64  `json:"minutes,omitempty"`
	FromTime        int64  `json:"from_time,omitempty"`
	ToTime          int64  `json:"to_time,omitempty"`
	Line            int64  `json:"line,omitempty"`
	Pages           int    `json:"pages,omitempty"`
	MaxContentChars int    `json:"max_content_chars,omitempty"`
}

type traceTimelineEvent struct {
	Time         string `json:"time"`
	UnixMilli    int64  `json:"unix_milli"`
	Project      string `json:"project"`
	Service      string `json:"service,omitempty"`
	Operation    string `json:"operation,omitempty"`
	Level        string `json:"level,omitempty"`
	Code         string `json:"code,omitempty"`
	Message      string `json:"message,omitempty"`
	GapFromPrior int64  `json:"gap_from_prior_ms,omitempty"`
}

type traceTimelineResult struct {
	TraceID          string               `json:"trace_id"`
	From             int64                `json:"from"`
	To               int64                `json:"to"`
	ProjectsQueried  []string             `json:"projects_queried"`
	MatchedProjects  []string             `json:"matched_projects"`
	Events           []traceTimelineEvent `json:"events"`
	ObservedDuration int64                `json:"observed_duration_ms,omitempty"`
	Summary          string               `json:"summary"`
	Warnings         []string             `json:"warnings,omitempty"`
}

type orderPlanArgs struct {
	Identifier string `json:"identifier"`
	IssueType  string `json:"issue_type,omitempty"`
	Days       int    `json:"days,omitempty"`
}

type productImageArgs struct {
	ProductID       string `json:"product_id"`
	ProductName     string `json:"product_name,omitempty"`
	TargetImage     string `json:"target_image,omitempty"`
	Project         string `json:"project,omitempty"`
	ProjectAlias    string `json:"project_alias,omitempty"`
	Logstore        string `json:"logstore,omitempty"`
	Days            int    `json:"days,omitempty"`
	FromTime        int64  `json:"from_time,omitempty"`
	ToTime          int64  `json:"to_time,omitempty"`
	Line            int64  `json:"line,omitempty"`
	Pages           int    `json:"pages,omitempty"`
	MaxContentChars int    `json:"max_content_chars,omitempty"`
}

type imageEvent struct {
	Time           string   `json:"time"`
	UnixTime       int64    `json:"unix_time"`
	Operation      string   `json:"operation"`
	Service        string   `json:"service,omitempty"`
	Container      string   `json:"container,omitempty"`
	TraceID        string   `json:"trace_id,omitempty"`
	Title          string   `json:"title,omitempty"`
	ProductID      string   `json:"product_id,omitempty"`
	DsersProductID string   `json:"dsers_product_id,omitempty"`
	StoreID        string   `json:"store_id,omitempty"`
	AppID          string   `json:"app_id,omitempty"`
	Status         string   `json:"status,omitempty"`
	MainImageURL   string   `json:"main_image_url,omitempty"`
	ImageURLs      []string `json:"image_urls,omitempty"`
}

type imageTransition struct {
	Time         string `json:"time"`
	UnixTime     int64  `json:"unix_time"`
	FromImageURL string `json:"from_image_url,omitempty"`
	ToImageURL   string `json:"to_image_url,omitempty"`
	Service      string `json:"service,omitempty"`
	TraceID      string `json:"trace_id,omitempty"`
}

type productImageResult struct {
	ProductID        string            `json:"product_id"`
	ProductName      string            `json:"product_name,omitempty"`
	Project          string            `json:"project"`
	Logstore         string            `json:"logstore"`
	From             int64             `json:"from"`
	To               int64             `json:"to"`
	TargetImage      string            `json:"target_image,omitempty"`
	Summary          string            `json:"summary"`
	EarliestTarget   *imageEvent       `json:"earliest_target,omitempty"`
	PreviousImage    *imageEvent       `json:"previous_image,omitempty"`
	LastTransition   *imageTransition  `json:"last_transition,omitempty"`
	Transitions      []imageTransition `json:"transitions"`
	DistinctTimeline []imageEvent      `json:"distinct_timeline"`
	QueriedTerms     []string          `json:"queried_terms"`
}

func main() {
	keyURL := flag.String("key-url", envDefault("ALIYUN_KEY_URL", defaultKeyURL), "HTTP endpoint that returns SLS credentials")
	keyCacheTTL := flag.Duration("key-cache-ttl", defaultKeyTTL, "duration to cache fetched SLS credentials in memory")
	envFile := flag.String("env-file", "", "optional fallback env file containing SLS credentials")
	allowEnvFallback := flag.Bool("allow-env-fallback", false, "allow loading SLS credentials from env file/environment if key-url is unavailable")
	flag.Parse()

	fmt.Fprintf(os.Stderr, "%s started\n", serverName)

	server := &mcpServer{
		envFile:          *envFile,
		keyURL:           *keyURL,
		keyCacheTTL:      *keyCacheTTL,
		allowEnvFallback: *allowEnvFallback,
	}
	if err := server.serve(context.Background(), os.Stdin, os.Stdout); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintf(os.Stderr, "%s exited: %v\n", serverName, err)
		os.Exit(1)
	}
}

func (s *mcpServer) serve(ctx context.Context, in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	writer := bufio.NewWriter(out)
	for {
		payload, mode, err := readMessage(reader)
		if err != nil {
			return err
		}
		if s.stdioMode == "" {
			s.stdioMode = mode
		}

		var req rpcRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			continue
		}

		result, rpcErr, respond := s.handle(ctx, req)
		if !respond {
			continue
		}

		resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
		if rpcErr != nil {
			resp.Error = rpcErr
		} else {
			resp.Result = result
		}
		if err := writeMessage(writer, resp, s.stdioMode); err != nil {
			return err
		}
	}
}

func (s *mcpServer) handle(ctx context.Context, req rpcRequest) (any, *rpcError, bool) {
	switch req.Method {
	case "initialize":
		protocolVersion := "2024-11-05"
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if len(req.Params) > 0 && json.Unmarshal(req.Params, &params) == nil && params.ProtocolVersion != "" {
			protocolVersion = params.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{
					"listChanged": false,
				},
				"resources": map[string]any{
					"subscribe":   false,
					"listChanged": false,
				},
			},
			"serverInfo": map[string]string{
				"name":    serverName,
				"version": serverVersion,
			},
		}, nil, req.ID != nil
	case "notifications/initialized":
		return nil, nil, false
	case "ping":
		return map[string]any{}, nil, req.ID != nil
	case "tools/list":
		return map[string]any{"tools": listTools()}, nil, req.ID != nil
	case "tools/call":
		result, err := s.callTool(ctx, req.Params)
		if err != nil {
			return nil, &rpcError{Code: -32000, Message: err.Error()}, req.ID != nil
		}
		return result, nil, req.ID != nil
	case "resources/list":
		return map[string]any{"resources": listResources()}, nil, req.ID != nil
	case "resources/read":
		result, err := readResource(req.Params)
		if err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}, req.ID != nil
		}
		return result, nil, req.ID != nil
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method}, req.ID != nil
	}
}

func readMessage(r *bufio.Reader) ([]byte, string, error) {
	contentLength := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, "", err
		}
		line = strings.TrimRight(line, "\r\n")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "{") {
			return []byte(trimmed), "jsonl", nil
		}
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil {
				return nil, "", err
			}
			contentLength = n
		}
	}
	if contentLength < 0 {
		return nil, "", errors.New("missing Content-Length header")
	}
	buf := make([]byte, contentLength)
	_, err := io.ReadFull(r, buf)
	return buf, "headers", err
}

func writeMessage(w *bufio.Writer, v any, mode string) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if mode == "jsonl" {
		if _, err := w.Write(payload); err != nil {
			return err
		}
		if err := w.WriteByte('\n'); err != nil {
			return err
		}
		return w.Flush()
	}
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	return w.Flush()
}

func (s *mcpServer) callTool(ctx context.Context, params json.RawMessage) (any, error) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, err
	}
	if len(call.Arguments) == 0 {
		call.Arguments = []byte("{}")
	}

	switch call.Name {
	case "sls_query_all", "order_price_history", "order_price_audit", "order_context", "resolve_user_emails":
		return s.callBusinessTool(ctx, call.Name, call.Arguments)
	case "sls_query":
		var args slsQueryArgs
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		svc, err := s.getSLS(ctx)
		if err != nil {
			return toolError(err), nil
		}
		result, err := svc.query(ctx, args)
		if err != nil {
			return toolError(err), nil
		}
		return toolJSON(result), nil
	case "sls_inspect_index":
		var args slsInspectIndexArgs
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		svc, err := s.getSLS(ctx)
		if err != nil {
			return toolError(err), nil
		}
		result, err := svc.inspectIndex(args)
		if err != nil {
			return toolError(err), nil
		}
		return toolJSON(result), nil
	case "trace_timeline":
		var args traceTimelineArgs
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		svc, err := s.getSLS(ctx)
		if err != nil {
			return toolError(err), nil
		}
		result, err := svc.traceTimeline(ctx, args)
		if err != nil {
			return toolError(err), nil
		}
		return toolJSON(result), nil
	case "order_log_plan":
		var args orderPlanArgs
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		return toolJSON(buildOrderPlan(args)), nil
	case "product_image_change":
		var args productImageArgs
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		svc, err := s.getSLS(ctx)
		if err != nil {
			return toolError(err), nil
		}
		result, err := svc.productImageChange(ctx, args)
		if err != nil {
			return toolError(err), nil
		}
		return toolJSON(result), nil
	default:
		return nil, fmt.Errorf("unknown tool: %s", call.Name)
	}
}

func toolJSON(v any) map[string]any {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return toolError(err)
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": false,
	}
}

func toolError(err error) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": err.Error()}},
		"isError": true,
	}
}

func listTools() []map[string]any {
	return append([]map[string]any{
		{
			"name":        "sls_query",
			"description": "Run a narrow Alibaba Cloud SLS query. It classifies raw, SQL/SPL analysis, and trace queries and returns an execution receipt.",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"query"},
				"properties": map[string]any{
					"query":             map[string]any{"type": "string", "description": "SLS search or query-and-analysis expression."},
					"project":           map[string]any{"type": "string", "description": "Explicit SLS project name. Overrides project_alias."},
					"project_alias":     map[string]any{"type": "string", "enum": []string{"dsers", "cc338", "dianshi", "shopify", "aofei", "傲飞", "c195"}},
					"logstore":          map[string]any{"type": "string", "default": defaultLogstore},
					"minutes":           map[string]any{"type": "integer", "default": 60},
					"from_time":         map[string]any{"type": "integer", "description": "Unix seconds. Overrides minutes when supplied with to_time."},
					"to_time":           map[string]any{"type": "integer", "description": "Unix seconds."},
					"line":              map[string]any{"type": "integer", "default": 100, "maximum": 100},
					"offset":            map[string]any{"type": "integer", "default": 0},
					"reverse":           map[string]any{"type": "boolean", "default": false},
					"max_content_chars": map[string]any{"type": "integer", "description": "Trim each string value to this length. 0 disables trimming."},
					"mode":              map[string]any{"type": "string", "enum": []string{"auto", "raw", "analysis", "trace"}, "default": "auto", "description": "Optional query mode. auto detects SQL/SPL and trace IDs."},
				},
			},
		},
		{
			"name":        "sls_inspect_index",
			"description": "Inspect the SLS LogStore index configuration before composing a field query or SQL analysis. Metadata is cached in process memory for 15 minutes.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project":       map[string]any{"type": "string", "description": "Explicit SLS project name. Overrides project_alias."},
					"project_alias": map[string]any{"type": "string", "enum": []string{"dsers", "cc338", "dianshi", "shopify", "aofei", "傲飞", "c195"}},
					"logstore":      map[string]any{"type": "string", "default": defaultLogstore},
					"refresh":       map[string]any{"type": "boolean", "default": false, "description": "Bypass the in-memory index metadata cache."},
				},
			},
		},
		{
			"name":        "trace_timeline",
			"description": "Search an exact trace ID across DSers and DianShi projects in parallel, then return a chronological call timeline and observed duration.",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"trace_id"},
				"properties": map[string]any{
					"trace_id":          map[string]any{"type": "string"},
					"project":           map[string]any{"type": "string", "description": "Optional explicit project. Without it, both DSers and DianShi projects are searched."},
					"project_alias":     map[string]any{"type": "string", "enum": []string{"dsers", "cc338", "dianshi", "shopify", "aofei", "傲飞", "c195"}},
					"logstore":          map[string]any{"type": "string", "default": defaultLogstore},
					"minutes":           map[string]any{"type": "integer", "default": 1440},
					"from_time":         map[string]any{"type": "integer"},
					"to_time":           map[string]any{"type": "integer"},
					"line":              map[string]any{"type": "integer", "default": 100, "maximum": 100},
					"pages":             map[string]any{"type": "integer", "default": 5, "maximum": 20},
					"max_content_chars": map[string]any{"type": "integer", "description": "Trim the extracted message only. 0 keeps the default limit."},
				},
			},
		},
		{
			"name":        "order_log_plan",
			"description": "Return the required DSers/DianShi order log query plan and reporting checklist for an identifier.",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"identifier"},
				"properties": map[string]any{
					"identifier": map[string]any{"type": "string"},
					"issue_type": map[string]any{"type": "string", "enum": []string{"status", "price", "address", "error", "generic"}},
					"days":       map[string]any{"type": "integer", "default": 7},
				},
			},
		},
		{
			"name":        "product_image_change",
			"description": "Find product image URL timeline and the first SLS event that uses a target image.",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"product_id"},
				"properties": map[string]any{
					"product_id":        map[string]any{"type": "string"},
					"product_name":      map[string]any{"type": "string"},
					"target_image":      map[string]any{"type": "string", "description": "Optional image filename or URL to locate."},
					"project":           map[string]any{"type": "string"},
					"project_alias":     map[string]any{"type": "string", "enum": []string{"dianshi", "shopify", "aofei", "傲飞", "c195", "dsers", "cc338"}},
					"logstore":          map[string]any{"type": "string", "default": defaultLogstore},
					"days":              map[string]any{"type": "integer", "default": 45},
					"from_time":         map[string]any{"type": "integer"},
					"to_time":           map[string]any{"type": "integer"},
					"line":              map[string]any{"type": "integer", "default": 100, "maximum": 100},
					"pages":             map[string]any{"type": "integer", "default": 5},
					"max_content_chars": map[string]any{"type": "integer"},
				},
			},
		},
	}, businessToolDefinitions()...)
}

func listResources() []map[string]any {
	return []map[string]any{
		{"uri": "logs://price-audit-rules", "name": "Price audit evidence rules", "description": "Price stages, completeness and attribution contract.", "mimeType": "text/markdown"},
		{
			"uri":         "logs://rules",
			"name":        "DSers log query rules",
			"description": "Project routing, order identifier contract, and interpretation rules.",
			"mimeType":    "text/markdown",
		},
		{
			"uri":         "logs://order-fields",
			"name":        "Order log fields",
			"description": "Canonical DSers, DianShi, Shopify field mappings and required answer checklist.",
			"mimeType":    "text/markdown",
		},
	}
}

func readResource(params json.RawMessage) (any, error) {
	var req struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, err
	}
	var text string
	switch req.URI {
	case "logs://price-audit-rules":
		text = priceAuditRules
	case "logs://rules":
		text = logsRules
	case "logs://order-fields":
		text = orderFields
	default:
		return nil, fmt.Errorf("unknown resource uri: %s", req.URI)
	}
	return map[string]any{
		"contents": []map[string]any{{
			"uri":      req.URI,
			"mimeType": "text/markdown",
			"text":     text,
		}},
	}, nil
}

func (s *mcpServer) getSLS(ctx context.Context) (*slsService, error) {
	cfg, err := s.getSLSKeyConfig(ctx)
	if err != nil {
		return nil, err
	}
	if s.sls != nil && s.slsKey == cfg {
		return s.sls, nil
	}
	client := sls.CreateNormalInterface(cfg.Endpoint, cfg.AccessKeyID, cfg.AccessKeySecret, cfg.SecurityToken)
	client.SetUserAgent(serverName + "/" + serverVersion)
	s.sls = &slsService{
		client:          client,
		defaultProject:  cfg.Project,
		defaultLogstore: cfg.Logstore,
		indexCache:      make(map[string]cachedIndex),
		indexCacheTTL:   defaultIndexTTL,
	}
	s.slsKey = cfg
	return s.sls, nil
}

func (s *mcpServer) getSLSKeyConfig(ctx context.Context) (*slsKeyConfig, error) {
	ttl := s.keyCacheTTL
	if ttl == 0 {
		ttl = defaultKeyTTL
	}
	if s.cachedSLSKey != nil && (ttl < 0 || time.Since(s.cachedSLSKeyAt) < ttl) {
		return s.cachedSLSKey, nil
	}

	var failures []string
	if strings.TrimSpace(s.keyURL) != "" {
		cfg, err := s.fetchSLSKeyConfig(ctx)
		if err == nil {
			s.cachedSLSKey = cfg
			s.cachedSLSKeyAt = time.Now()
			return cfg, nil
		}
		failures = append(failures, err.Error())
	}
	if s.allowEnvFallback {
		cfg, err := s.envSLSKeyConfig()
		if err == nil {
			s.cachedSLSKey = cfg
			s.cachedSLSKeyAt = time.Now()
			return cfg, nil
		}
		failures = append(failures, err.Error())
	}

	if len(failures) > 0 {
		return nil, fmt.Errorf("failed to load SLS credentials: %s", strings.Join(failures, "; "))
	}
	return nil, errors.New("missing SLS credential source: set --key-url or enable --allow-env-fallback")
}

func (s *mcpServer) envSLSKeyConfig() (*slsKeyConfig, error) {
	if err := loadEnvFile(s.envFile); err != nil {
		return nil, err
	}
	cfg := &slsKeyConfig{
		Endpoint:        os.Getenv("ALIYUN_SLS_ENDPOINT"),
		AccessKeyID:     os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_ID"),
		AccessKeySecret: os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET"),
		SecurityToken:   os.Getenv("ALIBABA_CLOUD_SECURITY_TOKEN"),
		Project:         os.Getenv("ALIYUN_SLS_PROJECT"),
		Logstore:        os.Getenv("ALIYUN_SLS_LOGSTORE"),
	}
	if err := normalizeSLSKeyConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (s *mcpServer) fetchSLSKeyConfig(ctx context.Context) (*slsKeyConfig, error) {
	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}

	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, s.keyURL, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid key endpoint: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", serverName+"/"+serverVersion)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("key endpoint request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("key endpoint returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read key endpoint response: %w", err)
	}
	cfg, err := parseSLSKeyResponse(body)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func parseSLSKeyResponse(body []byte) (*slsKeyConfig, error) {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return nil, errors.New("key endpoint returned an empty response")
	}

	var decoded any
	if err := json.Unmarshal([]byte(text), &decoded); err == nil {
		if cfg, ok := extractSLSKeyConfig(decoded); ok {
			if err := normalizeSLSKeyConfig(cfg); err != nil {
				return nil, err
			}
			return cfg, nil
		}
	}

	if strings.Contains(text, "=") {
		cfg := keyConfigFromEnvMap(parseEnvLines(text))
		if err := normalizeSLSKeyConfig(cfg); err != nil {
			return nil, err
		}
		return cfg, nil
	}
	return nil, errors.New("key endpoint response missing required SLS credential fields")
}

func extractSLSKeyConfig(v any) (*slsKeyConfig, bool) {
	switch typed := v.(type) {
	case map[string]any:
		cfg := &slsKeyConfig{
			Endpoint:        lookupString(typed, "ALIYUN_SLS_ENDPOINT", "aliyunSlsEndpoint", "slsEndpoint", "endpoint", "Endpoint"),
			AccessKeyID:     lookupString(typed, "ALIBABA_CLOUD_ACCESS_KEY_ID", "accessKeyId", "accessKeyID", "access_key_id", "AccessKeyId", "AccessKeyID", "keyId", "ak", "akId"),
			AccessKeySecret: lookupString(typed, "ALIBABA_CLOUD_ACCESS_KEY_SECRET", "accessKeySecret", "access_key_secret", "AccessKeySecret", "keySecret", "sk", "akSecret"),
			SecurityToken:   lookupString(typed, "ALIBABA_CLOUD_SECURITY_TOKEN", "securityToken", "security_token", "SecurityToken", "stsToken", "token"),
			Project:         lookupString(typed, "ALIYUN_SLS_PROJECT", "aliyunSlsProject", "slsProject", "project", "Project"),
			Logstore:        lookupString(typed, "ALIYUN_SLS_LOGSTORE", "aliyunSlsLogstore", "slsLogstore", "logstore", "Logstore"),
		}
		if cfg.Endpoint != "" && cfg.AccessKeyID != "" && cfg.AccessKeySecret != "" {
			return cfg, true
		}

		for _, key := range []string{"data", "result", "payload", "credentials", "credential", "aliyunKey", "aliyun_key"} {
			if child, ok := typed[key]; ok {
				if cfg, ok := extractSLSKeyConfig(child); ok {
					return cfg, true
				}
			}
		}
		for _, child := range typed {
			if cfg, ok := extractSLSKeyConfig(child); ok {
				return cfg, true
			}
		}
	case []any:
		for _, child := range typed {
			if cfg, ok := extractSLSKeyConfig(child); ok {
				return cfg, true
			}
		}
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return nil, false
		}
		if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") || strings.HasPrefix(text, `"`) {
			var decoded any
			if err := json.Unmarshal([]byte(text), &decoded); err == nil {
				return extractSLSKeyConfig(decoded)
			}
		}
		if strings.Contains(text, "=") {
			cfg := keyConfigFromEnvMap(parseEnvLines(text))
			return cfg, cfg.Endpoint != "" && cfg.AccessKeyID != "" && cfg.AccessKeySecret != ""
		}
	}
	return nil, false
}

func lookupString(m map[string]any, aliases ...string) string {
	for _, alias := range aliases {
		if v, ok := m[alias]; ok {
			if s := anyToString(v); s != "" {
				return s
			}
		}
	}
	for key, v := range m {
		for _, alias := range aliases {
			if strings.EqualFold(key, alias) {
				if s := anyToString(v); s != "" {
					return s
				}
			}
		}
	}
	return ""
}

func anyToString(v any) string {
	switch typed := v.(type) {
	case string:
		return strings.TrimSpace(typed)
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func keyConfigFromEnvMap(values map[string]string) *slsKeyConfig {
	return &slsKeyConfig{
		Endpoint:        values["ALIYUN_SLS_ENDPOINT"],
		AccessKeyID:     values["ALIBABA_CLOUD_ACCESS_KEY_ID"],
		AccessKeySecret: values["ALIBABA_CLOUD_ACCESS_KEY_SECRET"],
		SecurityToken:   values["ALIBABA_CLOUD_SECURITY_TOKEN"],
		Project:         values["ALIYUN_SLS_PROJECT"],
		Logstore:        values["ALIYUN_SLS_LOGSTORE"],
	}
}

func normalizeSLSKeyConfig(cfg *slsKeyConfig) error {
	cfg.Endpoint = strings.TrimSpace(cfg.Endpoint)
	cfg.AccessKeyID = strings.TrimSpace(cfg.AccessKeyID)
	cfg.AccessKeySecret = strings.TrimSpace(cfg.AccessKeySecret)
	cfg.SecurityToken = strings.TrimSpace(cfg.SecurityToken)
	cfg.Project = strings.TrimSpace(cfg.Project)
	cfg.Logstore = strings.TrimSpace(cfg.Logstore)
	if cfg.Project == "" {
		cfg.Project = projectDSers
	}
	if cfg.Logstore == "" {
		cfg.Logstore = defaultLogstore
	}
	if cfg.Endpoint == "" || cfg.AccessKeyID == "" || cfg.AccessKeySecret == "" {
		return errors.New("missing SLS credentials: key endpoint must return endpoint, access key id, and access key secret")
	}
	return nil
}

func (s *slsService) query(ctx context.Context, args slsQueryArgs) (*slsQueryResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if strings.TrimSpace(args.Query) == "" {
		return nil, errors.New("query is required")
	}
	project := resolveProject(args.ProjectAlias, args.Project, s.defaultProject)
	logstore := args.Logstore
	if logstore == "" {
		logstore = s.defaultLogstore
	}
	from, to := resolveWindow(args.FromTime, args.ToTime, args.Minutes, 60)
	line := clampInt64(args.Line, 100, 1, 100)
	if args.Offset < 0 {
		args.Offset = 0
	}
	mode, err := resolveQueryMode(args.Mode, args.Query)
	if err != nil {
		return nil, err
	}
	query, receipt := prepareQuery(args, mode, from, to, line)

	resp, err := s.client.GetLogs(project, logstore, args.Topic, from, to, query, receipt.EffectiveLine, receipt.EffectiveOffset, receipt.EffectiveReverse)
	if err != nil {
		return nil, err
	}
	if !resp.IsComplete() {
		receipt.Warnings = append(receipt.Warnings, "SLS returned an incomplete result set; narrow the time window or add indexed fields before concluding.")
	}
	rows := trimRows(resp.Logs, args.MaxContentChars)
	return &slsQueryResult{
		Completed: resp.IsComplete(),
		Count:     resp.Count,
		From:      from,
		To:        to,
		Project:   project,
		Logstore:  logstore,
		Query:     query,
		Rows:      rows,
		Receipt:   receipt,
	}, nil
}

func (s *slsService) inspectIndex(args slsInspectIndexArgs) (*slsIndexResult, error) {
	project := resolveProject(args.ProjectAlias, args.Project, s.defaultProject)
	logstore := args.Logstore
	if logstore == "" {
		logstore = s.defaultLogstore
	}
	key := project + "/" + logstore
	cacheTTL := s.indexCacheTTL
	if cacheTTL <= 0 {
		cacheTTL = defaultIndexTTL
	}

	entry, cached := s.indexCache[key]
	if args.Refresh || !cached || time.Since(entry.fetchedAt) >= cacheTTL {
		index, err := s.client.GetIndex(project, logstore)
		if err != nil {
			return nil, fmt.Errorf("failed to inspect SLS index for %s/%s: %w", project, logstore, err)
		}
		entry = cachedIndex{value: index, fetchedAt: time.Now()}
		s.indexCache[key] = entry
		cached = false
	}

	fields := make([]slsIndexField, 0, len(entry.value.Keys))
	for name, field := range entry.value.Keys {
		jsonKeys := make([]string, 0, len(field.JsonKeys))
		for jsonKey := range field.JsonKeys {
			jsonKeys = append(jsonKeys, jsonKey)
		}
		sort.Strings(jsonKeys)
		fields = append(fields, slsIndexField{
			Name:          name,
			Type:          field.Type,
			CaseSensitive: field.CaseSensitive,
			DocValue:      field.DocValue,
			JSONKeys:      jsonKeys,
		})
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })

	result := &slsIndexResult{
		Project:   project,
		Logstore:  logstore,
		Cached:    cached,
		FetchedAt: entry.fetchedAt.UTC().Format(time.RFC3339),
		Fields:    fields,
		QueryHints: []string{
			"Use field syntax for listed fields. Numeric long/double fields support range comparisons such as status>=500.",
			"Use raw text search only when the target field is unknown or no suitable field index exists.",
			"Keep raw detail queries narrow; use SQL/SPL with LIMIT for aggregation, Top N, or time-series analysis.",
		},
	}
	if entry.value.Line != nil {
		result.FullText = true
		result.FullTextToken = entry.value.Line.Token
	}
	return result, nil
}

func resolveQueryMode(requested, query string) (string, error) {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested == "" || requested == "auto" {
		if strings.Contains(query, "|") {
			return "analysis", nil
		}
		if containsLikelyTraceID(query) {
			return "trace", nil
		}
		return "raw", nil
	}
	if requested != "raw" && requested != "analysis" && requested != "trace" {
		return "", fmt.Errorf("unsupported mode %q: use auto, raw, analysis, or trace", requested)
	}
	if requested == "analysis" && !strings.Contains(query, "|") {
		return "", errors.New("analysis mode requires an SLS query-and-analysis expression containing |")
	}
	return requested, nil
}

func containsLikelyTraceID(value string) bool {
	for _, candidate := range traceIDPattern.FindAllString(strings.ToLower(value), -1) {
		if strings.ContainsAny(candidate, "abcdef") {
			return true
		}
	}
	return false
}

func prepareQuery(args slsQueryArgs, mode string, from, to, line int64) (string, queryReceipt) {
	query := strings.TrimSpace(args.Query)
	receipt := queryReceipt{
		Mode:             mode,
		RequestedMode:    strings.TrimSpace(args.Mode),
		EffectiveLine:    line,
		EffectiveOffset:  args.Offset,
		EffectiveReverse: args.Reverse,
	}
	if mode == "analysis" {
		receipt.EffectiveOffset = 0
		receipt.EffectiveReverse = false
		if !sqlLimitPattern.MatchString(query) {
			query += " LIMIT 100"
			receipt.Warnings = append(receipt.Warnings, "Added LIMIT 100 to prevent an unbounded SQL/SPL analysis result.")
		}
		receipt.Warnings = append(receipt.Warnings, "For SQL/SPL analysis, LIMIT and ORDER BY in the query control the result; offset and reverse are ignored.")
	}
	if mode == "raw" && strings.TrimSpace(query) == "*" && to-from > 24*60*60 {
		receipt.Warnings = append(receipt.Warnings, "This is a full-text scan over more than 24 hours; add an exact ID, operation, or indexed field to reduce latency and scan cost.")
	}
	if mode == "trace" {
		receipt.Warnings = append(receipt.Warnings, "For a complete cross-project trace timeline, prefer the trace_timeline tool.")
	}
	return query, receipt
}

func (s *slsService) traceTimeline(ctx context.Context, args traceTimelineArgs) (*traceTimelineResult, error) {
	traceID := strings.TrimSpace(args.TraceID)
	if traceID == "" {
		return nil, errors.New("trace_id is required")
	}
	logstore := args.Logstore
	if logstore == "" {
		logstore = s.defaultLogstore
	}
	from, to := resolveWindow(args.FromTime, args.ToTime, args.Minutes, 24*60)
	line := clampInt64(args.Line, 100, 1, 100)
	pages := args.Pages
	if pages <= 0 {
		pages = 5
	}
	if pages > 20 {
		pages = 20
	}

	projects := traceProjects(args.ProjectAlias, args.Project, s.defaultProject)
	type projectResult struct {
		project string
		rows    []map[string]string
		err     error
	}
	results := make(chan projectResult, len(projects))
	var group sync.WaitGroup
	for _, project := range projects {
		group.Add(1)
		go func(project string) {
			defer group.Done()
			rows, err := s.collectTraceRows(ctx, project, logstore, traceID, from, to, line, pages)
			results <- projectResult{project: project, rows: rows, err: err}
		}(project)
	}
	group.Wait()
	close(results)

	result := &traceTimelineResult{
		TraceID:         traceID,
		From:            from,
		To:              to,
		ProjectsQueried: projects,
	}
	seenProjects := map[string]bool{}
	seenEvents := map[string]bool{}
	for batch := range results {
		if batch.err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s query failed: %v", batch.project, batch.err))
			continue
		}
		if len(batch.rows) > 0 && !seenProjects[batch.project] {
			seenProjects[batch.project] = true
			result.MatchedProjects = append(result.MatchedProjects, batch.project)
		}
		for _, row := range batch.rows {
			event := parseTraceTimelineEvent(row, batch.project, args.MaxContentChars)
			key := fmt.Sprintf("%s|%d|%s|%s|%s", event.Project, event.UnixMilli, event.Service, event.Operation, event.Message)
			if seenEvents[key] {
				continue
			}
			seenEvents[key] = true
			result.Events = append(result.Events, event)
		}
	}

	sort.Slice(result.Events, func(i, j int) bool {
		if result.Events[i].UnixMilli == result.Events[j].UnixMilli {
			if result.Events[i].Project == result.Events[j].Project {
				return result.Events[i].Operation < result.Events[j].Operation
			}
			return result.Events[i].Project < result.Events[j].Project
		}
		return result.Events[i].UnixMilli < result.Events[j].UnixMilli
	})
	for i := range result.Events {
		if i > 0 && result.Events[i].UnixMilli > 0 && result.Events[i-1].UnixMilli > 0 {
			result.Events[i].GapFromPrior = result.Events[i].UnixMilli - result.Events[i-1].UnixMilli
		}
	}
	if len(result.Events) == 0 {
		result.Summary = "No matching trace logs were found in the queried projects and time window."
		return result, nil
	}
	first := result.Events[0].UnixMilli
	last := result.Events[len(result.Events)-1].UnixMilli
	if first > 0 && last >= first {
		result.ObservedDuration = last - first
	}
	result.Summary = fmt.Sprintf("Found %d events across %d project(s). Observed duration is the interval between the earliest and latest log timestamps, not a tracing span duration.", len(result.Events), len(result.MatchedProjects))
	return result, nil
}

func traceProjects(alias, explicit, fallback string) []string {
	if strings.TrimSpace(explicit) != "" || strings.TrimSpace(alias) != "" {
		return []string{resolveProject(alias, explicit, fallback)}
	}
	return []string{projectDSers, projectDianShi}
}

func (s *slsService) collectTraceRows(ctx context.Context, project, logstore, traceID string, from, to, line int64, pages int) ([]map[string]string, error) {
	rows := make([]map[string]string, 0, int(line)*pages)
	query := quoteTerm(traceID)
	for page := 0; page < pages; page++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		response, err := s.client.GetLogs(project, logstore, "", from, to, query, line, int64(page)*line, false)
		if err != nil {
			return nil, err
		}
		rows = append(rows, response.Logs...)
		if int64(len(response.Logs)) < line {
			break
		}
	}
	return rows, nil
}

func parseTraceTimelineEvent(row map[string]string, project string, maxMessageChars int) traceTimelineEvent {
	content := rowContent(row)
	unixMilli, timeText := rowTimestampMillis(row, content)
	message := firstNonEmpty(
		row["message"],
		firstRegex(content, `"(?:message|msg|error)":"([^"\\]+)`),
		firstRegex(content, `(?:message|msg|error):\\?"([^"\\]+)`),
	)
	if message == "" {
		message = firstNonEmpty(firstRegex(content, `(rpc error: code = [^\n]+)`), firstRegex(content, `(?i)((?:error|exception|panic)[:=][^\n]+)`))
	}
	if maxMessageChars <= 0 {
		maxMessageChars = 500
	}
	if len(message) > maxMessageChars {
		message = message[:maxMessageChars] + "...<truncated>"
	}
	return traceTimelineEvent{
		Time:      timeText,
		UnixMilli: unixMilli,
		Project:   project,
		Service: firstNonEmpty(
			row["service"],
			row["service_name"],
			firstRegex(content, `"service(?:\.name)?":"([^"\\]+)`),
		),
		Operation: extractTraceOperation(row, content),
		Level:     firstNonEmpty(row["level"], firstRegex(content, `"level":"([^"\\]+)`)),
		Code:      firstNonEmpty(row["code"], firstRegex(content, `"code":?"?([0-9A-Za-z_]+)`)),
		Message:   message,
	}
}

func extractTraceOperation(row map[string]string, content string) string {
	candidates := []string{
		row["operation"],
		row["method"],
		firstRegex(content, `"(?:operation|method)":"([^"\\]+)`),
		firstRegex(content, `(?:operation|method)[=:]\\?"?([^"\\,\s]+)`),
		firstRegex(content, `(/(?:[A-Za-z0-9_.]+Service|[A-Za-z0-9_.]+Server)/[A-Za-z0-9_]+)`),
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || strings.Contains(candidate, ".go:") || strings.Contains(candidate, "zap.go") {
			continue
		}
		return candidate
	}
	return ""
}

func (s *slsService) productImageChange(ctx context.Context, args productImageArgs) (*productImageResult, error) {
	if strings.TrimSpace(args.ProductID) == "" {
		return nil, errors.New("product_id is required")
	}
	project := resolveProject(args.ProjectAlias, args.Project, projectDianShi)
	logstore := args.Logstore
	if logstore == "" {
		logstore = s.defaultLogstore
	}
	minutes := int64(args.Days) * 24 * 60
	if minutes <= 0 {
		minutes = 45 * 24 * 60
	}
	from, to := resolveWindow(args.FromTime, args.ToTime, minutes, 45*24*60)
	line := clampInt64(args.Line, 100, 1, 100)
	pages := args.Pages
	if pages <= 0 {
		pages = 5
	}
	if pages > 20 {
		pages = 20
	}

	ops := []string{"UpdateMerchantProduct", "UpsertMyProduct", "UpsertSellerProduct", "SellerProductWebhook"}
	var events []imageEvent
	var queried []string
	for _, op := range ops {
		query := strings.Join([]string{quoteTerm(args.ProductID), quoteTerm(op)}, " ")
		queried = append(queried, query)
		batch, err := s.collectImageEvents(ctx, project, logstore, args.ProductID, args.ProductName, query, op, from, to, line, pages, args.MaxContentChars, false)
		if err != nil {
			return nil, err
		}
		events = append(events, batch...)
	}

	events = dedupeEvents(events)
	sort.Slice(events, func(i, j int) bool {
		if events[i].UnixTime == events[j].UnixTime {
			return events[i].TraceID < events[j].TraceID
		}
		return events[i].UnixTime < events[j].UnixTime
	})

	var earliestTarget *imageEvent
	if args.TargetImage != "" {
		for _, op := range ops {
			query := strings.Join([]string{quoteTerm(args.ProductID), quoteTerm(args.TargetImage), quoteTerm(op)}, " ")
			queried = append(queried, query)
			batch, err := s.collectImageEvents(ctx, project, logstore, args.ProductID, args.ProductName, query, op, from, to, line, pages, args.MaxContentChars, false)
			if err != nil {
				return nil, err
			}
			events = append(events, batch...)
		}
		events = dedupeEvents(events)
		sort.Slice(events, func(i, j int) bool {
			if events[i].UnixTime == events[j].UnixTime {
				return events[i].TraceID < events[j].TraceID
			}
			return events[i].UnixTime < events[j].UnixTime
		})
		for i := range events {
			if eventContainsImage(events[i], args.TargetImage) {
				copy := events[i]
				earliestTarget = &copy
				break
			}
		}
		if earliestTarget != nil && earliestTarget.UnixTime > from {
			for _, op := range ops {
				query := strings.Join([]string{quoteTerm(args.ProductID), quoteTerm(op)}, " ")
				queried = append(queried, query+" reverse_before_target")
				batch, err := s.collectImageEvents(ctx, project, logstore, args.ProductID, args.ProductName, query, op, from, earliestTarget.UnixTime, line, pages, args.MaxContentChars, true)
				if err != nil {
					return nil, err
				}
				events = append(events, batch...)
			}
		}
	}

	events = dedupeEvents(events)
	sort.Slice(events, func(i, j int) bool {
		if events[i].UnixTime == events[j].UnixTime {
			return events[i].TraceID < events[j].TraceID
		}
		return events[i].UnixTime < events[j].UnixTime
	})
	timeline := distinctImageTimeline(events)
	transitions := buildTransitions(timeline)
	var previousImage *imageEvent
	if args.TargetImage != "" {
		for i := range events {
			if eventContainsImage(events[i], args.TargetImage) {
				copy := events[i]
				earliestTarget = &copy
				break
			}
		}
		if earliestTarget != nil {
			for i := len(timeline) - 1; i >= 0; i-- {
				if timeline[i].UnixTime >= earliestTarget.UnixTime {
					continue
				}
				if timeline[i].MainImageURL != "" && timeline[i].MainImageURL != earliestTarget.MainImageURL {
					copy := timeline[i]
					previousImage = &copy
					break
				}
			}
		}
	}

	var lastTransition *imageTransition
	if len(transitions) > 0 {
		copy := transitions[len(transitions)-1]
		lastTransition = &copy
	}

	summary := "No image URL change found in the queried window."
	if earliestTarget != nil && previousImage != nil {
		summary = fmt.Sprintf("Target image first appeared at %s; previous distinct image was seen at %s.", earliestTarget.Time, previousImage.Time)
	} else if earliestTarget != nil {
		summary = fmt.Sprintf("Target image first appeared at %s; no previous distinct image was found in the queried rows.", earliestTarget.Time)
	} else if lastTransition != nil {
		summary = fmt.Sprintf("Latest image transition in queried rows happened at %s.", lastTransition.Time)
	}

	return &productImageResult{
		ProductID:        args.ProductID,
		ProductName:      args.ProductName,
		Project:          project,
		Logstore:         logstore,
		From:             from,
		To:               to,
		TargetImage:      args.TargetImage,
		Summary:          summary,
		EarliestTarget:   earliestTarget,
		PreviousImage:    previousImage,
		LastTransition:   lastTransition,
		Transitions:      transitions,
		DistinctTimeline: timeline,
		QueriedTerms:     queried,
	}, nil
}

func (s *slsService) collectImageEvents(ctx context.Context, project, logstore, productID, productName, query, op string, from, to, line int64, pages int, maxContentChars int, reverse bool) ([]imageEvent, error) {
	var events []imageEvent
	for page := 0; page < pages; page++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		resp, err := s.client.GetLogs(project, logstore, "", from, to, query, line, int64(page)*line, reverse)
		if err != nil {
			return nil, err
		}
		for _, row := range trimRows(resp.Logs, maxContentChars) {
			evt := parseImageEvent(row, op, productID)
			if evt.MainImageURL == "" && len(evt.ImageURLs) == 0 {
				continue
			}
			if productName != "" && evt.Title == "" {
				evt.Title = productName
			}
			events = append(events, evt)
		}
		if int64(len(resp.Logs)) < line {
			break
		}
	}
	return events, nil
}

func buildOrderPlan(args orderPlanArgs) map[string]any {
	days := args.Days
	if days <= 0 {
		days = 7
	}
	identifier := strings.TrimSpace(args.Identifier)
	issueType := strings.TrimSpace(args.IssueType)
	if issueType == "" {
		issueType = "generic"
	}

	terms := []string{"fullInsertData", "OrderHook", "CreateOrder", "supplier_order_status", "ITEM_SUPPLY_STATUS"}
	switch issueType {
	case "price":
		terms = []string{"BatchUpdateOrderPrice", "ORDER_CHANGE", "GetOrderQuote", "GetOrdersDetail"}
	case "address":
		terms = []string{"Address", "shipping_address", "OrderHook", "CreateOrder", "UpsertOrder"}
	case "error":
		terms = []string{"error", "exception", "trace_id"}
	}

	queries := make([]string, 0, len(terms))
	for _, term := range terms {
		queries = append(queries, quoteTerm(identifier)+" "+quoteTerm(term))
	}
	return map[string]any{
		"identifier": identifier,
		"issue_type": issueType,
		"days":       days,
		"projects": map[string]string{
			"dsers":           projectDSers,
			"dianshi_shopify": projectDianShi,
		},
		"logstore": defaultLogstore,
		"required_identifier_checks": []string{
			"Check Shopify order_name/name/order_no/SellerOrderName.",
			"Check DianShi order_sn.",
			"Check DSers order_id/DsersOrderId/dsers_order_id with parent context.",
		},
		"queries": queries,
		"reporting_checklist": []string{
			"List seller-side, DSers, and DianShi/supplier order numbers and statuses.",
			"List store name, agent ID, DSers user_id, store_id, seller_app_id, supplier_app_id.",
			"List item title, item_id, status, supply source, supplier order ID, and fulfillment source.",
			"Treat supply_source=0 or SupplySource:0 as unmapped.",
			"Treat supplier_app_id=1658073296948719616 as Agency.",
			"Keep missing fields explicit as not found instead of guessing.",
		},
	}
}

func parseImageEvent(row map[string]string, op string, productID string) imageEvent {
	content := rowContent(row)
	unix, timeText := rowTimestamp(row, content)
	imageURLs := extractShopifyURLs(content)
	mainImages := extractMainImages(content)
	main := ""
	if len(mainImages) > 0 {
		main = mainImages[0]
	} else if len(imageURLs) > 0 {
		main = imageURLs[0]
	}
	return imageEvent{
		Time:           timeText,
		UnixTime:       unix,
		Operation:      op,
		Service:        firstRegex(content, `"service.name":"([^"]+)"`),
		Container:      firstNonEmpty(row["_container_name_"], firstRegex(content, `"service.id":"([^"]+)"`)),
		TraceID:        firstRegex(content, `"trace_id":"([^"]+)"`),
		Title:          firstNonEmpty(firstRegex(content, `product_title:\\"([^"\\]+)`), firstRegex(content, `"ProductName":"([^"]+)`), firstRegex(content, `title:\\"([^"\\]+)`)),
		ProductID:      productID,
		DsersProductID: firstRegex(content, `dsers_product_id:([0-9]+)`),
		StoreID:        firstRegex(content, `store_id:([0-9]+)`),
		AppID:          firstRegex(content, `app_id:([0-9]+)`),
		Status:         firstRegex(content, `status:([A-Za-z0-9_]+)`),
		MainImageURL:   main,
		ImageURLs:      imageURLs,
	}
}

func rowContent(row map[string]string) string {
	if row["content"] != "" {
		return row["content"]
	}
	keys := make([]string, 0, len(row))
	for k := range row {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(row[k])
		b.WriteByte('\n')
	}
	return b.String()
}

func rowTimestamp(row map[string]string, content string) (int64, string) {
	candidates := []string{row["_time_"], row["__time__"], firstRegex(content, `"ts":"([^"]+)"`)}
	for _, s := range candidates {
		if s == "" {
			continue
		}
		if ts, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return ts.Unix(), ts.Format("2006-01-02 15:04:05")
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			if n > 1_000_000_000_000 {
				n = n / 1000
			}
			return n, time.Unix(n, 0).Format("2006-01-02 15:04:05")
		}
	}
	if s := firstRegex(content, `"ts":([0-9]{10})(?:\.[0-9]+)?`); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n, time.Unix(n, 0).Format("2006-01-02 15:04:05")
		}
	}
	return 0, ""
}

func rowTimestampMillis(row map[string]string, content string) (int64, string) {
	candidates := []string{
		row["_time_"],
		row["__time__"],
		row["time"],
		firstRegex(content, `"ts":"([^"]+)`),
		firstRegex(content, `"timestamp":"([^"]+)`),
	}
	for _, value := range candidates {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if timestamp, err := time.Parse(time.RFC3339Nano, value); err == nil {
			return timestamp.UnixMilli(), timestamp.In(beijingLocation).Format("2006-01-02 15:04:05.000 MST")
		}
		if numeric, err := strconv.ParseInt(value, 10, 64); err == nil {
			if numeric < 1_000_000_000_000 {
				numeric *= 1000
			}
			return numeric, time.UnixMilli(numeric).In(beijingLocation).Format("2006-01-02 15:04:05.000 MST")
		}
	}
	if value := firstRegex(content, `"ts":([0-9]{10,13})(?:\.[0-9]+)?`); value != "" {
		if numeric, err := strconv.ParseInt(value, 10, 64); err == nil {
			if numeric < 1_000_000_000_000 {
				numeric *= 1000
			}
			return numeric, time.UnixMilli(numeric).In(beijingLocation).Format("2006-01-02 15:04:05.000 MST")
		}
	}
	return 0, ""
}

func extractShopifyURLs(content string) []string {
	re := regexp.MustCompile(`https?:\\?/\\?/cdn\.shopify\.com[^\s"\\]+(?:\\?[^\s"\\]*)?`)
	matches := re.FindAllString(content, -1)
	seen := map[string]bool{}
	var urls []string
	for _, m := range matches {
		u := normalizeImageURL(m)
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		urls = append(urls, u)
	}
	return urls
}

func extractMainImages(content string) []string {
	patterns := []string{
		`main_img_url:\\"([^"\\]+)`,
		`"main_img_url":"([^"]+)`,
		`MainImgUrl:\\"([^"\\]+)`,
		`"ImageUrl":"(https?:\\?/\\?/cdn\.shopify\.com[^"]+)`,
	}
	seen := map[string]bool{}
	var urls []string
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		for _, match := range re.FindAllStringSubmatch(content, -1) {
			if len(match) < 2 {
				continue
			}
			u := normalizeImageURL(match[1])
			if u == "" || seen[u] {
				continue
			}
			seen[u] = true
			urls = append(urls, u)
		}
	}
	return urls
}

func normalizeImageURL(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, `\/`, `/`)
	s = strings.Trim(s, `\" ,]}>)`)
	return s
}

func dedupeEvents(events []imageEvent) []imageEvent {
	seen := map[string]bool{}
	var out []imageEvent
	for _, e := range events {
		key := fmt.Sprintf("%d|%s|%s|%s|%s", e.UnixTime, e.Operation, e.Service, e.TraceID, e.MainImageURL)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, e)
	}
	return out
}

func distinctImageTimeline(events []imageEvent) []imageEvent {
	var out []imageEvent
	last := ""
	for _, e := range events {
		if e.MainImageURL == "" {
			continue
		}
		if e.MainImageURL == last {
			continue
		}
		out = append(out, e)
		last = e.MainImageURL
	}
	return out
}

func buildTransitions(timeline []imageEvent) []imageTransition {
	var transitions []imageTransition
	for i := 1; i < len(timeline); i++ {
		transitions = append(transitions, imageTransition{
			Time:         timeline[i].Time,
			UnixTime:     timeline[i].UnixTime,
			FromImageURL: timeline[i-1].MainImageURL,
			ToImageURL:   timeline[i].MainImageURL,
			Service:      timeline[i].Service,
			TraceID:      timeline[i].TraceID,
		})
	}
	return transitions
}

func eventContainsImage(e imageEvent, target string) bool {
	target = strings.ToLower(target)
	if target == "" {
		return false
	}
	if strings.Contains(strings.ToLower(e.MainImageURL), target) {
		return true
	}
	for _, u := range e.ImageURLs {
		if strings.Contains(strings.ToLower(u), target) {
			return true
		}
	}
	return false
}

func trimRows(rows []map[string]string, maxChars int) []map[string]string {
	if maxChars <= 0 {
		return rows
	}
	out := make([]map[string]string, 0, len(rows))
	for _, row := range rows {
		next := make(map[string]string, len(row))
		for k, v := range row {
			if len(v) > maxChars {
				v = v[:maxChars] + "...<truncated>"
			}
			next[k] = v
		}
		out = append(out, next)
	}
	return out
}

func resolveWindow(from, to, minutes, defaultMinutes int64) (int64, int64) {
	if to <= 0 {
		to = time.Now().Unix()
	}
	if from <= 0 {
		if minutes <= 0 {
			minutes = defaultMinutes
		}
		from = to - minutes*60
	}
	return from, to
}

func resolveProject(alias, explicit, fallback string) string {
	if explicit != "" {
		return explicit
	}
	switch strings.ToLower(alias) {
	case "dsers", "cc338":
		return projectDSers
	case "dianshi", "shopify", "aofei", "傲飞", "c195":
		return projectDianShi
	}
	return fallback
}

func clampInt64(v, def, min, max int64) int64 {
	if v == 0 {
		v = def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func quoteTerm(s string) string {
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func firstRegex(s, pattern string) string {
	re := regexp.MustCompile(pattern)
	match := re.FindStringSubmatch(s)
	if len(match) < 2 {
		return ""
	}
	return strings.ReplaceAll(match[1], `\/`, `/`)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func loadEnvFile(path string) error {
	if path == "" {
		return nil
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = cleanEnvValue(value)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, value)
		}
	}
	return scanner.Err()
}

func parseEnvLines(text string) map[string]string {
	values := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		values[key] = cleanEnvValue(value)
	}
	return values
}

func cleanEnvValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		first := value[0]
		last := value[len(value)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

const logsRules = `# Logs MCP Rules

Projects:
- DSers domain logs: k8s-log-cc338bd1e67fa4d8e9be4ad1e9435670a
- DianShi / Shopify / order-name lifecycle logs: k8s-log-c19589a718db24dbb835525e4e8f2c2a0
- Default logstore: dsers-app

Order identifier contract:
- order_name means Shopify order name such as #167591.
- order_sn means DianShi order number.
- order_id means DSers order ID only when the parent context confirms DSers.
- Query order_name, order_sn, and order_id contexts before interpreting a bare identifier.

Query strategy:
- Start with exact identifiers plus high-signal terms.
- Prefer raw search rows for long payloads because SQL analysis can truncate content.
- After finding a trace_id, query by exact trace_id to reconstruct the call chain.
- Do not treat an empty result in one project as final until the paired project has been checked.

Interpretation:
- supply_source=0 or SupplySource:0 means unmapped.
- supplier_app_id=1658073296948719616 means Agency.
- Report missing fields explicitly instead of guessing.
`

const orderFields = `# Order Log Fields

Canonical fields:
- Shopify order name: order_name, name, order_no, SellerOrderName.
- DianShi order_sn: order_sn.
- Shopify seller order ID: seller_order_id, ThirdPartyOrderId, seller-side order_id.
- DSers order ID: DsersOrderId, dsers_order_id, or order_id only with DSers parent context.
- DianShi order ID: order_list.order_id.
- Supplier order ID: SupplyOrderId, supplier_order_id.
- Store ID: StoreId, store_id, store_ids.
- DSers user ID: UserId, user_id.
- Agent ID: agency_id, agent_id, supply_store_id for Agency supply.

High-signal terms:
- Search index state: fullInsertData, DsersOrderId, PlatformOrderStatus.
- Shopify/source order: seller_order, seller_order_id, order_no, financial_status, fulfillment_status.
- DianShi creation: CreateOrder, dsers_order_id, order_list, supplier_app_id.
- Price changes: BatchUpdateOrderPrice, ORDER_CHANGE, GetOrderQuote, GetOrdersDetail.
- Supplier status: supplier_order_status, ITEM_SUPPLY_STATUS, SupplyOrderId, SupplySource.
- Error tracing: exact error text plus ID, then exact trace_id.

Required order answer:
- Seller-side, DSers, and DianShi/supplier order numbers and statuses.
- Store name, agent ID, DSers user_id, and store_id.
- Item title, item_id, status, supplier/source per item.
- Any discovered trace_id for failures or changes.
`
