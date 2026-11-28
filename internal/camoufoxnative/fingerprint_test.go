package camoufoxnative

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestApplyLocaleTimezoneAcceptLanguageWeight(t *testing.T) {
	cases := []struct {
		locale string
		want   string
	}{
		{locale: "en-US", want: "en-US,en;q=0.5"},
		{locale: "zh-CN", want: "zh-CN,zh;q=0.5"},
		{locale: "ja", want: "ja"},
	}
	for _, tc := range cases {
		config := make(map[string]any)
		applyLocaleTimezone(config, tc.locale, "UTC")
		if got, _ := config["headers.Accept-Language"].(string); got != tc.want {
			t.Fatalf("applyLocaleTimezone(%q) Accept-Language = %q, want %q", tc.locale, got, tc.want)
		}
	}
}

func TestNormalizeCamoufoxConfigMigrationsAndPreservation(t *testing.T) {
	renderer := "ANGLE (Intel, Intel(R) HD Graphics Direct3D11 vs_5_0 ps_5_0), or similar"
	config := map[string]any{
		"headers.Accept-Language":       "en-US,en;q=0.9",
		"navigator.language":            "en-US",
		"navigator.hardwareConcurrency": 6,
		"screen.width":                  1920,
		"screen.height":                 1080,
		"screen.availWidth":             1920,
		"screen.availHeight":            1040, // Win10 taskbar (40)
		"window.devicePixelRatio":       1.0,
		"window.outerWidth":             1938,
		"window.outerHeight":            1058,
		"window.innerWidth":             1920,
		"window.innerHeight":            955,
		"window.screenX":                -9,
		"window.screenY":                -9,
		"webGl:vendor":                  "Google Inc. (Intel)",
		"webGl:renderer":                renderer,
		"AudioContext:sampleRate":       44100,
		"AudioContext:maxChannelCount":  2,
		quotaTemporaryLimitKey:          30 << 20,
		"battery:charging":              true,
		"battery:chargingTime":          120,
		"battery:dischargingTime":       3600,
		"battery:level":                 0.85,
	}

	normalizeCamoufoxConfig(config)

	if got := config["headers.Accept-Language"]; got != "en-US,en;q=0.5" {
		t.Fatalf("Accept-Language = %v, want en-US,en;q=0.5", got)
	}
	for key := range config {
		if strings.HasPrefix(key, "battery:") {
			t.Fatalf("unexpected battery key remaining after normalize: %s", key)
		}
	}
	if got, _ := integer(config, "screen.availHeight"); got != 1040 {
		t.Fatalf("Win10 availHeight = %d, want preserved 1040", got)
	}
	if got, _ := integer(config, "navigator.hardwareConcurrency"); got != 6 {
		t.Fatalf("hardwareConcurrency mutated to %d, want 6", got)
	}
	if got := config["webGl:renderer"]; got != renderer {
		t.Fatalf("webGl:renderer mutated to %v, want %q", got, renderer)
	}
	if got, _ := integer(config, "AudioContext:sampleRate"); got != 44100 {
		t.Fatalf("AudioContext:sampleRate = %d, want 44100", got)
	}
	if got, _ := integer(config, quotaTemporaryLimitKey); got != 30<<20 {
		t.Fatalf("quotaTemporaryLimitKey = %d, want %d", got, 30<<20)
	}

	// Missing or invalid availHeight falls back to height - 48.
	delete(config, "screen.availHeight")
	normalizeCamoufoxConfig(config)
	if got, _ := integer(config, "screen.availHeight"); got != 1080-48 {
		t.Fatalf("fallback availHeight = %d, want %d", got, 1080-48)
	}
}

func TestHardwareTiersConsistency(t *testing.T) {
	allowedQuotas := []int{20 << 20, 30 << 20, 50 << 20, 80 << 20, 100 << 20}
	seenTaskbar := map[int]bool{}
	seenSampleRate := map[int]bool{}
	seenQuota := map[int]bool{}

	for _, tier := range []hardwareTier{hardwareTierLow, hardwareTierMainstream, hardwareTierHigh} {
		for range 120 {
			profile := drawWindowsHardwareProfileForTier(tier)
			vendor, _ := profile.webgl["webGl:vendor"].(string)
			renderer, _ := profile.webgl["webGl:renderer"].(string)
			if vendor == "" || renderer == "" {
				t.Fatalf("tier %d produced empty webgl vendor=%q renderer=%q", tier, vendor, renderer)
			}
			if tier != hardwareTierLow && isLowEndWebGLRenderer(vendor, renderer) {
				t.Fatalf("tier %d paired with low-end renderer %q (%s)", tier, renderer, vendor)
			}
			switch tier {
			case hardwareTierLow:
				if profile.concurrency > 8 {
					t.Fatalf("low tier concurrency = %d, want <= 8", profile.concurrency)
				}
			case hardwareTierMainstream:
				if profile.concurrency < 4 || profile.concurrency > 16 {
					t.Fatalf("mainstream tier concurrency = %d, want in [4,16]", profile.concurrency)
				}
			case hardwareTierHigh:
				if profile.concurrency < 12 {
					t.Fatalf("high tier concurrency = %d, want >= 12", profile.concurrency)
				}
				if profile.display.width < 1920 {
					t.Fatalf("high tier display width = %d, want >= 1920", profile.display.width)
				}
			}
			if profile.taskbar != windowsTaskbarWin10 && profile.taskbar != windowsTaskbarWin11 {
				t.Fatalf("unexpected taskbar height %d", profile.taskbar)
			}
			if profile.audioRate != 48000 && profile.audioRate != 44100 {
				t.Fatalf("unexpected AudioContext sampleRate %d", profile.audioRate)
			}
			if profile.audioChans != 2 {
				t.Fatalf("unexpected AudioContext maxChannelCount %d", profile.audioChans)
			}
			if !slices.Contains(allowedQuotas, profile.storageLimit) {
				t.Fatalf("unexpected storageLimit %d", profile.storageLimit)
			}
			seenTaskbar[profile.taskbar] = true
			seenSampleRate[profile.audioRate] = true
			seenQuota[profile.storageLimit] = true
		}
	}

	if !seenTaskbar[windowsTaskbarWin10] || !seenTaskbar[windowsTaskbarWin11] {
		t.Fatalf("expected both Win10 (40) and Win11 (48) taskbar heights, got %v", seenTaskbar)
	}
	if !seenSampleRate[48000] || !seenSampleRate[44100] {
		t.Fatalf("expected both 48000 and 44100 sample rates, got %v", seenSampleRate)
	}
	if len(seenQuota) < 3 {
		t.Fatalf("expected diverse storage quota tiers, got %v", seenQuota)
	}
}

func TestBuildCamoufoxConfigOutputs(t *testing.T) {
	allowedQuotas := []int{20 << 20, 30 << 20, 50 << 20, 80 << 20, 100 << 20}
	for range 30 {
		config, err := buildCamoufoxConfig(camoufoxFirefoxMajor, "en-US", "America/New_York")
		if err != nil {
			t.Fatalf("buildCamoufoxConfig failed: %v", err)
		}
		if got := config["headers.Accept-Language"]; got != "en-US,en;q=0.5" {
			t.Fatalf("Accept-Language = %v, want en-US,en;q=0.5", got)
		}
		for key := range config {
			if strings.HasPrefix(key, "battery:") {
				t.Fatalf("buildCamoufoxConfig must not emit %s", key)
			}
		}
		rate, ok := integer(config, "AudioContext:sampleRate")
		if !ok || (rate != 48000 && rate != 44100) {
			t.Fatalf("AudioContext:sampleRate = %v, want 48000 or 44100", config["AudioContext:sampleRate"])
		}
		chans, ok := integer(config, "AudioContext:maxChannelCount")
		if !ok || chans != 2 {
			t.Fatalf("AudioContext:maxChannelCount = %v, want 2", config["AudioContext:maxChannelCount"])
		}
		quota, ok := integer(config, quotaTemporaryLimitKey)
		if !ok || !slices.Contains(allowedQuotas, quota) {
			t.Fatalf("quotaTemporaryLimitKey = %v, want one of %v", config[quotaTemporaryLimitKey], allowedQuotas)
		}
		height, _ := integer(config, "screen.height")
		availHeight, _ := integer(config, "screen.availHeight")
		if diff := height - availHeight; diff != windowsTaskbarWin10 && diff != windowsTaskbarWin11 {
			t.Fatalf("screen.height(%d) - screen.availHeight(%d) = %d, want 40 or 48", height, availHeight, diff)
		}
		cores, _ := integer(config, "navigator.hardwareConcurrency")
		vendor, _ := config["webGl:vendor"].(string)
		renderer, _ := config["webGl:renderer"].(string)
		if cores >= 12 && isLowEndWebGLRenderer(vendor, renderer) {
			t.Fatalf("high core count %d paired with low-end renderer %q (%s)", cores, renderer, vendor)
		}
	}
}

func TestCamoufoxEnvironmentStripsInternalQuotaKey(t *testing.T) {
	config := map[string]any{
		"AudioContext:sampleRate":      44100,
		"AudioContext:maxChannelCount": 2,
		quotaTemporaryLimitKey:         80 << 20,
	}
	env, err := camoufoxEnvironment(config)
	if err != nil {
		t.Fatalf("camoufoxEnvironment failed: %v", err)
	}
	chunks := map[int]string{}
	for _, item := range env {
		name, value, ok := strings.Cut(item, "=")
		if !ok || !strings.HasPrefix(name, "CAMOU_CONFIG_") {
			continue
		}
		index, err := strconv.Atoi(strings.TrimPrefix(name, "CAMOU_CONFIG_"))
		if err != nil {
			t.Fatalf("unexpected chunk name %q: %v", name, err)
		}
		chunks[index] = value
	}
	var joined strings.Builder
	for index := 1; index <= len(chunks); index++ {
		joined.WriteString(chunks[index])
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(joined.String()), &decoded); err != nil {
		t.Fatalf("unmarshal CAMOU_CONFIG payload: %v", err)
	}
	if _, exists := decoded[quotaTemporaryLimitKey]; exists {
		t.Fatalf("CAMOU_CONFIG must not include internal key %q", quotaTemporaryLimitKey)
	}
	if got, _ := integer(decoded, "AudioContext:sampleRate"); got != 44100 {
		t.Fatalf("CAMOU_CONFIG AudioContext:sampleRate = %d, want 44100", got)
	}
	if got, _ := integer(decoded, "AudioContext:maxChannelCount"); got != 2 {
		t.Fatalf("CAMOU_CONFIG AudioContext:maxChannelCount = %d, want 2", got)
	}
	if _, stillPresent := config[quotaTemporaryLimitKey]; !stillPresent {
		t.Fatal("camoufoxEnvironment must not mutate caller config map")
	}
}

func TestWindowsStorageLimitFromConfig(t *testing.T) {
	if got := windowsStorageLimitFromConfig(map[string]any{}); got != windowsStorageLimit {
		t.Fatalf("missing quota returned %d, want default %d", got, windowsStorageLimit)
	}
	if got := windowsStorageLimitFromConfig(map[string]any{quotaTemporaryLimitKey: 0}); got != windowsStorageLimit {
		t.Fatalf("zero quota returned %d, want default %d", got, windowsStorageLimit)
	}
	if got := windowsStorageLimitFromConfig(map[string]any{quotaTemporaryLimitKey: 80 << 20}); got != 80<<20 {
		t.Fatalf("int quota returned %d, want %d", got, 80<<20)
	}
	if got := windowsStorageLimitFromConfig(map[string]any{quotaTemporaryLimitKey: float64(30 << 20)}); got != 30<<20 {
		t.Fatalf("float64 quota returned %d, want %d", got, 30<<20)
	}
}

func TestFirefoxPreferencesPingThreshold(t *testing.T) {
	prefs, err := firefoxPreferences("", "")
	if err != nil {
		t.Fatalf("firefoxPreferences failed: %v", err)
	}
	if got := prefs["network.http.http2.ping-threshold"]; got != 58 {
		t.Fatalf("network.http.http2.ping-threshold = %v, want 58", got)
	}
}

func TestLoadAccountCamoufoxConfigMigratesLegacyFile(t *testing.T) {
	dir := t.TempDir()
	storageState := filepath.Join(dir, "storage-state.json")
	fpPath := filepath.Join(dir, "camoufox-fingerprint.json")
	legacy := savedFingerprint{
		FirefoxVersion: camoufoxFirefoxMajor,
		Locale:         "en-US",
		Timezone:       "UTC",
		Config: map[string]any{
			"navigator.language":            "en-US",
			"headers.Accept-Language":       "en-US,en;q=0.9",
			"navigator.hardwareConcurrency": 8,
			"screen.width":                  1920,
			"screen.height":                 1080,
			"screen.availWidth":             1920,
			"screen.availHeight":            1032,
			"webGl:vendor":                  "Google Inc. (Intel)",
			"webGl:renderer":                "ANGLE (Intel, Intel(R) HD Graphics Direct3D11 vs_5_0 ps_5_0), or similar",
			"battery:charging":              true,
			"battery:dischargingTime":       999,
		},
	}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fpPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := loadAccountCamoufoxConfig(storageState, camoufoxFirefoxMajor, "en-US", "UTC")
	if err != nil {
		t.Fatalf("loadAccountCamoufoxConfig failed: %v", err)
	}
	if got := loaded["headers.Accept-Language"]; got != "en-US,en;q=0.5" {
		t.Fatalf("loaded Accept-Language = %v, want en-US,en;q=0.5", got)
	}
	for key := range loaded {
		if strings.HasPrefix(key, "battery:") {
			t.Fatalf("loaded legacy config still contains %s", key)
		}
	}
	if got, _ := integer(loaded, "navigator.hardwareConcurrency"); got != 8 {
		t.Fatalf("legacy hardwareConcurrency mutated to %d, want 8", got)
	}
	if got, _ := integer(loaded, "screen.availHeight"); got != 1032 {
		t.Fatalf("legacy availHeight mutated to %d, want 1032", got)
	}

	persistedBytes, err := os.ReadFile(fpPath)
	if err != nil {
		t.Fatal(err)
	}
	var persisted savedFingerprint
	if err := json.Unmarshal(persistedBytes, &persisted); err != nil {
		t.Fatal(err)
	}
	if got := persisted.Config["headers.Accept-Language"]; got != "en-US,en;q=0.5" {
		t.Fatalf("persisted Accept-Language = %v, want en-US,en;q=0.5", got)
	}
	if _, exists := persisted.Config["battery:charging"]; exists {
		t.Fatal("persisted legacy file still contains battery:charging")
	}
}
