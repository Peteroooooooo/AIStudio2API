package egress

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func resetExitCacheForTest() {
	exitCacheMu.Lock()
	exitCache = make(map[string]*exitCacheEntry)
	exitCacheMu.Unlock()
}

func TestEnvironmentCachesAndFallsBackToNeutral(t *testing.T) {
	resetExitCacheForTest()
	origURLs := lookupURLs
	t.Cleanup(func() {
		lookupURLs = origURLs
		resetExitCacheForTest()
	})

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ip":"1.2.3.4","country_code":"SG","timezone":"Asia/Singapore"}`))
	}))
	defer server.Close()

	lookupURLs = []string{server.URL}

	first, err := Environment(context.Background(), "", "", "")
	if err != nil {
		t.Fatalf("first Environment failed: %v", err)
	}
	if first.Country != "SG" || first.Timezone != "Asia/Singapore" || first.Locale != "en-SG" {
		t.Fatalf("unexpected first exit: %+v", first)
	}
	second, err := Environment(context.Background(), "", "", "")
	if err != nil {
		t.Fatalf("second Environment failed: %v", err)
	}
	if second != first {
		t.Fatalf("cached exit mismatch: got %+v, want %+v", second, first)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 1 GeoIP request with cache, got %d", calls.Load())
	}

	resetExitCacheForTest()
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer failServer.Close()
	lookupURLs = []string{failServer.URL}

	fallback, err := Environment(context.Background(), "", "", "")
	if err == nil {
		t.Fatal("expected error when GeoIP fails")
	}
	if fallback.Locale != fallbackLocale || fallback.Timezone != fallbackTimezone {
		t.Fatalf("expected neutral fallback (%s, %s), got (%s, %s)", fallbackLocale, fallbackTimezone, fallback.Locale, fallback.Timezone)
	}
}
