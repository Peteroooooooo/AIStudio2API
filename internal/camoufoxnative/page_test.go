package camoufoxnative

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// TestNormalizePromptNewlines 覆盖官网 textarea 把写入值规范化为 LF 的比较场景
func TestNormalizePromptNewlines(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{name: "lf", value: "a\nb", want: "a\nb"},
		{name: "crlf", value: "a\r\nb", want: "a\nb"},
		{name: "cr", value: "a\rb", want: "a\nb"},
		{name: "mixed", value: "a\r\nb\nc\rd", want: "a\nb\nc\nd"},
		{name: "empty", value: "", want: ""},
		{name: "no newline", value: "abc", want: "abc"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if got := normalizePromptNewlines(item.value); got != item.want {
				t.Fatalf("normalizePromptNewlines(%q) = %q, want %q", item.value, got, item.want)
			}
		})
	}
}

// TestNormalizePromptNewlinesMatchesTextareaValue 确认 CRLF 提示词与官网返回的 LF 值判定为同步
func TestNormalizePromptNewlinesMatchesTextareaValue(t *testing.T) {
	prompt := "line1\r\nline2\r\nline3"
	textarea := "line1\nline2\nline3"
	if prompt == textarea {
		t.Fatal("用例前提失效: CRLF 提示词不应与 textarea 值直接相等")
	}
	if normalizePromptNewlines(prompt) != normalizePromptNewlines(textarea) {
		t.Fatal("CRLF 提示词与 textarea LF 值应判定为已同步")
	}
}

func TestSamplePointerPointWithinSafeBox(t *testing.T) {
	target := pointerTarget{X: 200, Y: 100, W: 100, H: 40, VW: 1280, VH: 720}
	seenNonCenter := false
	for range 500 {
		point := samplePointerPoint(target)
		if point.X < 175 || point.X > 225 {
			t.Fatalf("sampled X=%d outside ±25%% box [175,225]", point.X)
		}
		if point.Y < 90 || point.Y > 110 {
			t.Fatalf("sampled Y=%d outside ±25%% box [90,110]", point.Y)
		}
		if point.X != target.X || point.Y != target.Y {
			seenNonCenter = true
		}
	}
	if !seenNonCenter {
		t.Fatal("expected sampled pointer points to vary around center")
	}

	tiny := pointerTarget{X: 10, Y: 10, W: 2, H: 2, VW: 1280, VH: 720}
	if got := samplePointerPoint(tiny); got != (pointerPoint{X: 10, Y: 10}) {
		t.Fatalf("tiny target sampled %+v, want {10 10}", got)
	}
}

func TestBuildPointerMoveActions(t *testing.T) {
	from := pointerPoint{X: 100, Y: 120}
	to := pointerPoint{X: 700, Y: 480}

	instant := buildPointerMoveActions(from, true, to, 0, 1280, 720)
	if len(instant) != 1 || instant[0]["duration"] != 0 || instant[0]["x"] != to.X || instant[0]["y"] != to.Y {
		t.Fatalf("move=0 actions = %#v, want single duration=0 move to %+v", instant, to)
	}

	seenSegments := map[int]bool{}
	seenDurations := map[int]bool{}
	seenArc := false
	for range 200 {
		actions := buildPointerMoveActions(from, true, to, pointerMoveDuration, 1280, 720)
		if len(actions) < 2 || len(actions) > 3 {
			t.Fatalf("expected 2-3 segments, got %d", len(actions))
		}
		seenSegments[len(actions)] = true
		totalDuration := 0
		for index, action := range actions {
			if action["type"] != "pointerMove" {
				t.Fatalf("action %d type = %v, want pointerMove", index, action["type"])
			}
			x := action["x"].(int)
			y := action["y"].(int)
			dur := action["duration"].(int)
			if x < 0 || x >= 1280 || y < 0 || y >= 720 {
				t.Fatalf("waypoint (%d,%d) out of viewport bounds", x, y)
			}
			if dur <= 0 {
				t.Fatalf("segment %d duration = %d, want > 0", index, dur)
			}
			totalDuration += dur
			if index == len(actions)-1 {
				if x != to.X || y != to.Y {
					t.Fatalf("final point = (%d,%d), want (%d,%d)", x, y, to.X, to.Y)
				}
			} else {
				// Check perpendicular distance from the line segment from -> to
				cross := (x-from.X)*(to.Y-from.Y) - (y-from.Y)*(to.X-from.X)
				if cross != 0 {
					seenArc = true
				}
			}
		}
		if totalDuration < 90 || totalDuration >= 200 {
			t.Fatalf("totalDuration = %d, want in [90,199]", totalDuration)
		}
		seenDurations[totalDuration] = true
	}
	if !seenSegments[2] || !seenSegments[3] {
		t.Fatalf("expected both 2 and 3 segment trajectories, got %v", seenSegments)
	}
	if len(seenDurations) < 5 {
		t.Fatalf("expected randomized durations, got %v", seenDurations)
	}
	if !seenArc {
		t.Fatal("expected curved waypoints off the straight line")
	}
}

func TestDismissKnownOverlaysUsesTrustedPointerClicks(t *testing.T) {
	if strings.Contains(locateOverlaysExpression, ".click(") {
		t.Fatal("locateOverlaysExpression must not call untrusted element.click()")
	}

	type recordedCommand struct {
		Method string
		Params map[string]any
	}

	runMock := func(t *testing.T, handler func(method string, params map[string]any) map[string]any) (*bidiClient, *[]recordedCommand, func()) {
		t.Helper()
		var commands []recordedCommand
		upgrader := websocket.Upgrader{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			for {
				var req struct {
					ID     int            `json:"id"`
					Method string         `json:"method"`
					Params map[string]any `json:"params"`
				}
				if err := conn.ReadJSON(&req); err != nil {
					return
				}
				commands = append(commands, recordedCommand{Method: req.Method, Params: req.Params})
				res := handler(req.Method, req.Params)
				_ = conn.WriteJSON(map[string]any{
					"id":     req.ID,
					"type":   "success",
					"result": res,
				})
			}
		}))
		wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			server.Close()
			t.Fatalf("dial mock BiDi server: %v", err)
		}
		client := newBiDiClient(conn, nil)
		cleanup := func() {
			_ = conn.Close()
			server.Close()
		}
		return client, &commands, cleanup
	}

	t.Run("no overlays", func(t *testing.T) {
		client, commands, cleanup := runMock(t, func(method string, params map[string]any) map[string]any {
			return map[string]any{"result": map[string]any{"type": "string", "value": "[]"}}
		})
		defer cleanup()

		clicked, err := dismissKnownOverlays(context.Background(), client, "ctx-none")
		if err != nil {
			t.Fatalf("dismissKnownOverlays failed: %v", err)
		}
		if clicked != 0 {
			t.Fatalf("clicked = %d, want 0", clicked)
		}
		if len(*commands) != 1 || (*commands)[0].Method != "script.evaluate" {
			t.Fatalf("unexpected commands: %#v", *commands)
		}
	})

	t.Run("two overlays clicked via trusted input.performActions", func(t *testing.T) {
		matchesJSON, _ := json.Marshal([]overlayMatch{
			{
				Selector: "ms-g1-welcome-dialog button",
				Target:   &pointerTarget{X: 600, Y: 400, W: 120, H: 40, VW: 1280, VH: 720},
			},
			{
				Selector: "#glue-cookie-notification-bar-1 .glue-cookie-notification-bar__reject",
			},
		})
		secondJSON, _ := json.Marshal(pointerTarget{X: 900, Y: 680, W: 100, H: 36, VW: 1280, VH: 720})

		client, commands, cleanup := runMock(t, func(method string, params map[string]any) map[string]any {
			switch method {
			case "script.evaluate":
				return map[string]any{"result": map[string]any{"type": "string", "value": string(matchesJSON)}}
			case "script.callFunction":
				return map[string]any{"result": map[string]any{"type": "string", "value": string(secondJSON)}}
			case "input.performActions":
				return map[string]any{}
			default:
				return map[string]any{}
			}
		})
		defer cleanup()

		clicked, err := dismissKnownOverlays(context.Background(), client, "ctx-two")
		if err != nil {
			t.Fatalf("dismissKnownOverlays failed: %v", err)
		}
		if clicked != 2 {
			t.Fatalf("clicked = %d, want 2", clicked)
		}
		performCount := 0
		for _, cmd := range *commands {
			if cmd.Method != "input.performActions" {
				continue
			}
			performCount++
			sources, _ := cmd.Params["actions"].([]any)
			if len(sources) != 1 {
				t.Fatalf("expected 1 pointer source, got %d", len(sources))
			}
			source, _ := sources[0].(map[string]any)
			if source["type"] != "pointer" {
				t.Fatalf("source type = %v, want pointer", source["type"])
			}
			steps, _ := source["actions"].([]any)
			moveCount, downCount, upCount := 0, 0, 0
			for _, rawStep := range steps {
				step, _ := rawStep.(map[string]any)
				switch step["type"] {
				case "pointerMove":
					moveCount++
				case "pointerDown":
					downCount++
				case "pointerUp":
					upCount++
				}
			}
			if moveCount < 2 || moveCount > 3 || downCount != 1 || upCount != 1 {
				t.Fatalf("unexpected pointer step counts: moves=%d down=%d up=%d", moveCount, downCount, upCount)
			}
		}
		if performCount != 2 {
			t.Fatalf("input.performActions count = %d, want 2", performCount)
		}
	})
}
