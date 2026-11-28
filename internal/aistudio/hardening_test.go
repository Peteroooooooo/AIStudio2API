package aistudio

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type stubStaticFetcher struct {
	mu    sync.Mutex
	calls map[string]int
	files map[string][]byte
}

func (s *stubStaticFetcher) FetchStatic(_ context.Context, _ string, address string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calls == nil {
		s.calls = make(map[string]int)
	}
	s.calls[address]++
	return append([]byte(nil), s.files[address]...), nil
}

type stubHiddenProbeTransport struct {
	calls atomic.Int32
}

func (t *stubHiddenProbeTransport) Do(_ context.Context, req RPCRequest) (*RPCResponse, error) {
	if req.Method == "CountTokens" {
		t.calls.Add(1)
		if strings.Contains(string(req.Body), `"models/gemini-hidden-404"`) {
			return nil, &RPCError{Method: "CountTokens", StatusCode: http.StatusNotFound, Message: "not found"}
		}
		return &RPCResponse{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{JSONProtobufContentType}},
			Body:       io.NopCloser(strings.NewReader(`[5]`)),
		}, nil
	}
	return &RPCResponse{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`[]`))}, nil
}

func TestResolveSafetySettingsMatchesOfficialDefaults(t *testing.T) {
	resolved, err := resolveSafetySettings(nil)
	if err != nil {
		t.Fatalf("resolveSafetySettings(nil) error: %v", err)
	}
	if len(resolved) != 0 {
		t.Fatalf("expected empty safety settings by default, got %+v", resolved)
	}

	_, buildBody, err := EncodeBuildGenerateRequest(GenerateRequest{
		Model:    "gemini-2.5-pro",
		Contents: []Content{{Role: RoleUser, Parts: []Part{{Text: "hello"}}}},
	}, GenerationDefaults{MaxOutputTokens: 8192}, false, true)
	if err != nil {
		t.Fatalf("EncodeBuildGenerateRequest error: %v", err)
	}
	if strings.Contains(string(buildBody), "safetySettings") {
		t.Fatalf("expected Build request to omit safetySettings when unspecified, got %s", buildBody)
	}

	wire, _, err := EncodeGenerateContentRequest(GenerateRequest{
		Model:    "gemini-2.5-pro",
		Contents: []Content{{Role: RoleUser, Parts: []Part{{Text: "hello"}}}},
	}, GenerationDefaults{MaxOutputTokens: 8192}, RequestContext{})
	if err != nil {
		t.Fatalf("EncodeGenerateContentRequest error: %v", err)
	}
	var fields []json.RawMessage
	if err := json.Unmarshal(wire, &fields); err != nil {
		t.Fatalf("unmarshal wire: %v", err)
	}
	if len(fields) > 2 && string(fields[2]) != "null" {
		t.Fatalf("expected wire[2] to be null when unspecified, got %s", fields[2])
	}

	override, err := resolveSafetySettings([]SafetySetting{
		{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "OFF"},
		{Category: "HARM_CATEGORY_HATE_SPEECH", Threshold: "HARM_BLOCK_THRESHOLD_UNSPECIFIED"},
	})
	if err != nil {
		t.Fatalf("resolveSafetySettings override error: %v", err)
	}
	if len(override) != 1 || override[0].Category != "HARM_CATEGORY_HARASSMENT" || override[0].Threshold != "OFF" {
		t.Fatalf("unexpected override result: %+v", override)
	}
}

func TestDiscoverHiddenModelsDeduplicatesProbesAcrossAccounts(t *testing.T) {
	bundleURL := "https://www.gstatic.com/_/test/excm=_b/ed=1/m=_b"
	chunkURL := "https://www.gstatic.com/_/test/exm=_b/excm=_b/ed=1/m=new_chat"
	bundleJS := []byte(`path:"prompts/new_chat",load:()=>loadModule("new_chat")`)
	chunkJS := []byte(`{id:"gemini-2.5",type:"chat",EA:["models/gemini-2.5"]},{id:"gemini-hidden",type:"chat",EA:["models/gemini-hidden"]} queryParams:{model:"gemini-2.5-pro"} queryParams:{model:"gemini-hidden-ok"} queryParams:{model:"gemini-hidden-404"}`)

	fetcher := &stubStaticFetcher{files: map[string][]byte{bundleURL: bundleJS, chunkURL: chunkJS}}
	transport := &stubHiddenProbeTransport{}
	client := NewClient(ClientOptions{Transport: transport, Static: fetcher})

	baseModel := Model{ID: "gemini-2.5-pro", Name: "gemini-2.5-pro", Methods: []string{"generateContent"}}
	makeCatalog := func() modelCatalog {
		return modelCatalog{
			models:  []Model{baseModel},
			entries: map[string]modelEntry{"gemini-2.5-pro": {model: baseModel}},
			bundle:  bundleURL,
		}
	}
	client.catalogMu.Lock()
	client.catalogs["acc1"] = makeCatalog()
	client.catalogs["acc2"] = makeCatalog()
	client.catalogMu.Unlock()

	var wg sync.WaitGroup
	results := make([]map[string]string, 2)
	errs := make([]error, 2)
	for i, acc := range []string{"acc1", "acc2"} {
		wg.Add(1)
		go func(idx int, accountID string) {
			defer wg.Done()
			results[idx], errs[idx] = client.DiscoverHiddenModels(context.Background(), accountID)
		}(i, acc)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("account %d DiscoverHiddenModels failed: %v", i, err)
		}
		if results[i]["gemini-hidden-ok"] != "gemini-2.5-pro" {
			t.Fatalf("account %d missing gemini-hidden-ok: %+v", i, results[i])
		}
		if _, exists := results[i]["gemini-hidden-404"]; exists {
			t.Fatalf("account %d unexpectedly included 404 model: %+v", i, results[i])
		}
	}
	if got := transport.calls.Load(); got != 2 {
		t.Fatalf("expected 2 CountTokens probes total across both accounts, got %d", got)
	}
	if fetcher.calls[bundleURL] != 1 || fetcher.calls[chunkURL] != 1 {
		t.Fatalf("expected 1 static fetch per resource, got %+v", fetcher.calls)
	}
}

func TestNavigationTimingMonotonicWithConnectionAndTLS(t *testing.T) {
	start := time.Now()
	timing := &connectionTiming{
		dialed:       true,
		dnsStart:     start.Add(2 * time.Millisecond),
		dnsDone:      start.Add(8 * time.Millisecond),
		connectStart: start.Add(9 * time.Millisecond),
		connectDone:  start.Add(25 * time.Millisecond),
		tlsStart:     start.Add(26 * time.Millisecond),
		tlsDone:      start.Add(52 * time.Millisecond),
	}
	entry, domLoading := navigationTiming(pageLoad{
		url:        "https://aistudio.google.com/prompts/new_chat",
		protocol:   "h2",
		started:    start,
		connection: timing,
		headersAt:  start.Add(95 * time.Millisecond),
		end:        120 * time.Millisecond,
		encoded:    18500,
		decoded:    92000,
	})
	documentTiming(entry, 140*time.Millisecond)

	ordered := []string{
		"fetchStart", "domainLookupStart", "domainLookupEnd",
		"connectStart", "secureConnectionStart", "connectEnd",
		"requestStart", "responseStart", "responseEnd",
		"domInteractive", "domContentLoadedEventStart", "domContentLoadedEventEnd",
		"domComplete", "loadEventStart", "loadEventEnd",
	}
	prev := 0
	for _, key := range ordered {
		val := entry[key].(int)
		if val < prev {
			t.Fatalf("non-monotonic timing at %s=%d < prev=%d (entry=%+v)", key, val, prev, entry)
		}
		prev = val
	}
	if entry["responseEnd"].(int) <= entry["responseStart"].(int) {
		t.Fatalf("expected responseEnd > responseStart, got %v <= %v", entry["responseEnd"], entry["responseStart"])
	}
	if domLoading < entry["responseStart"].(int) {
		t.Fatalf("expected domLoading >= responseStart, got %d < %d", domLoading, entry["responseStart"].(int))
	}
	if entry["transferSize"].(int) <= entry["encodedBodySize"].(int) {
		t.Fatalf("expected transferSize > encodedBodySize, got %v <= %v", entry["transferSize"], entry["encodedBodySize"])
	}
}

func TestPageAnalyticsStatePersistsCIDAndIncrementsSCT(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, analyticsStateFile)
	now := time.Unix(1700000000, 0)

	first := &pageAnalytics{statePath: statePath, shared: map[int]int64{}}
	firstTarget := first.initSession(now)
	if !firstTarget || !first.firstVisit || first.sct != 1 || first.cid == "" {
		t.Fatalf("unexpected first session state: firstTarget=%v firstVisit=%v sct=%d cid=%q", firstTarget, first.firstVisit, first.sct, first.cid)
	}

	second := &pageAnalytics{statePath: statePath, shared: map[int]int64{}}
	secondTarget := second.initSession(now.Add(time.Minute))
	if !secondTarget || second.firstVisit || second.sct != 2 || second.cid != first.cid {
		t.Fatalf("unexpected second session state: secondTarget=%v firstVisit=%v sct=%d cid=%q wantCid=%q", secondTarget, second.firstVisit, second.sct, second.cid, first.cid)
	}
}
