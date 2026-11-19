package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

type delayedQuotaService struct {
	aistudio.Service
}

func (delayedQuotaService) Generate(_ context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	events := make(chan aistudio.Event, 1)
	go func() {
		defer close(events)
		time.Sleep(streamHeartbeatInterval + time.Second)
		events <- aistudio.Event{Kind: aistudio.EventError, Err: &aistudio.AllCoolingError{
			ModelID: request.Model,
			Until:   time.Now().Add(time.Hour),
			Reason:  aistudio.DailyLimitKind + ": quota exhausted",
		}}
	}()
	return events, nil
}

func TestChatCompletionsReturnsDelayedQuotaAsHTTP429(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				configDefaults := config.Default()
				handler := NewHandler(delayedQuotaService{}, Config{ResponseModes: func() ResponseModes {
					return ResponseModes{NonstreamKeepalive: configDefaults.NonstreamKeepalive}
				}})
				body := fmt.Sprintf(`{"model":"gemini-test","stream":%t,"messages":[{"role":"user","content":"hello"}]}`, stream)
				request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)

				if response.Code != http.StatusTooManyRequests {
					t.Fatalf("HTTP status = %d, want 429, body: %s", response.Code, response.Body)
				}
				if retryAfter := response.Result().Header.Get("Retry-After"); retryAfter != "3600" {
					t.Errorf("Retry-After = %q, want 3600", retryAfter)
				}
				if shouldRetry := response.Result().Header.Get("x-should-retry"); shouldRetry != "false" {
					t.Errorf("x-should-retry = %q, want false", shouldRetry)
				}
				if !strings.Contains(response.Body.String(), `"code":"rate_limit_exceeded"`) {
					t.Errorf("expected an OpenAI rate limit error, got %s", response.Body)
				}
			})
		})
	}
}

func TestShortRateLimitAllowsAutomaticRetry(t *testing.T) {
	response := httptest.NewRecorder()
	writeOpenAIRequestError(response, &aistudio.RPCError{
		StatusCode: http.StatusTooManyRequests,
		RetryDelay: 10 * time.Second,
	})

	if got := response.Result().Header.Get("Retry-After"); got != "10" {
		t.Errorf("Retry-After = %q, want 10", got)
	}
	if got := response.Result().Header.Get("x-should-retry"); got != "" {
		t.Errorf("x-should-retry = %q, want unset", got)
	}
}
