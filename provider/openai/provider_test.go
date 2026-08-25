package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/JIAOZAI1/acore/model"
)

func TestNewRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr error
	}{
		{
			name:    "missing API key",
			config:  Config{Models: []model.Model{{ID: "gpt-test"}}},
			wantErr: ErrMissingAPIKey,
		},
		{
			name:    "no models",
			config:  Config{APIKey: "test-key"},
			wantErr: ErrNoModels,
		},
		{
			name:    "empty model ID",
			config:  Config{APIKey: "test-key", Models: []model.Model{{}}},
			wantErr: ErrInvalidModel,
		},
		{
			name: "provider mismatch",
			config: Config{APIKey: "test-key", Models: []model.Model{{
				ID: "gpt-test", Provider: "other",
			}}},
			wantErr: ErrInvalidModel,
		},
		{
			name: "API mismatch",
			config: Config{APIKey: "test-key", Models: []model.Model{{
				ID: "gpt-test", API: "responses",
			}}},
			wantErr: ErrInvalidModel,
		},
		{
			name: "duplicate model",
			config: Config{APIKey: "test-key", Models: []model.Model{
				{ID: "gpt-test"},
				{ID: "gpt-test"},
			}},
			wantErr: ErrInvalidModel,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := New(test.config)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("New() error = %v, want errors.Is(%v)", err, test.wantErr)
			}
		})
	}
}

func TestNewRejectsInvalidBaseURL(t *testing.T) {
	baseURLs := []string{
		"relative/path",
		"ftp://example.com/v1",
		"https:///v1",
		"https://user@example.com/v1",
		"https://example.com/v1?debug=true",
		"https://example.com/v1#fragment",
	}
	for _, baseURL := range baseURLs {
		t.Run(baseURL, func(t *testing.T) {
			_, err := New(Config{
				APIKey:  "test-key",
				BaseURL: baseURL,
				Models:  []model.Model{{ID: "gpt-test"}},
			})
			if err == nil {
				t.Fatalf("New() accepted invalid BaseURL %q", baseURL)
			}
		})
	}
}

func TestNewSnapshotsModelsAndHeaders(t *testing.T) {
	headers := http.Header{"X-Project": {"before"}}
	models := []model.Model{{
		ID:              "gpt-test",
		InputModalities: []string{"text", "image"},
	}}
	provider, err := New(Config{APIKey: " test-key ", Headers: headers, Models: models})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	headers.Set("X-Project", "after")
	models[0].ID = "changed"
	models[0].InputModalities[0] = "audio"

	got := provider.Models()
	if provider.ID() != ProviderID {
		t.Fatalf("ID() = %q, want %q", provider.ID(), ProviderID)
	}
	if len(got) != 1 || got[0].ID != "gpt-test" {
		t.Fatalf("Models() = %+v", got)
	}
	if got[0].Provider != ProviderID || got[0].API != APIChatCompletions {
		t.Fatalf("normalized model = %+v", got[0])
	}
	if got[0].InputModalities[0] != "text" {
		t.Fatalf("InputModalities = %v, want snapshot", got[0].InputModalities)
	}
	if provider.headers.Get("X-Project") != "before" {
		t.Fatalf("header snapshot = %q, want before", provider.headers.Get("X-Project"))
	}

	got[0].ID = "mutated"
	got[0].InputModalities[0] = "mutated"
	again := provider.Models()
	if again[0].ID != "gpt-test" || again[0].InputModalities[0] != "text" {
		t.Fatalf("Models() exposed internal data: %+v", again[0])
	}
}

func TestGenerateSendsExpectedRequest(t *testing.T) {
	requestSeen := false
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		defer request.Body.Close()
		if request.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", request.Method)
		}
		if request.URL.Path != "/gateway/v1/chat/completions" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		if got := request.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		if got := request.Header.Get("Accept"); got != "text/event-stream" {
			t.Errorf("Accept = %q", got)
		}
		if got := request.Header.Get("X-Project"); got != "project-1" {
			t.Errorf("X-Project = %q", got)
		}

		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body["model"] != "gpt-test" || body["stream"] != true {
			t.Errorf("request body = %#v", body)
		}
		requestSeen = true

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"text/event-stream; charset=utf-8"}},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"model\":\"gpt-test-2026\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
					"data: [DONE]\n\n",
			)),
		}, nil
	})}

	provider, selected := newHTTPTestProvider(t, "https://gateway.test/gateway/v1/", client, http.Header{
		"Authorization": {"Bearer wrong"},
		"Content-Type":  {"text/plain"},
		"Accept":        {"application/json"},
		"X-Project":     {"project-1"},
	})
	result, err := model.Complete(context.Background(), mustBind(t, provider, selected), model.Request{
		Context: model.Context{Messages: []model.Message{{Role: model.RoleUser, Content: []model.ContentBlock{{Kind: model.ContentText, Text: "hello"}}}}},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if result.ModelID != "gpt-test-2026" || result.ProviderID != ProviderID {
		t.Fatalf("result identity = %+v", result)
	}
	if !requestSeen {
		t.Fatal("server did not receive request")
	}
}

func TestGenerateReturnsStructuredAPIError(t *testing.T) {
	headers := make(http.Header)
	headers.Set("X-Request-ID", "req-123")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Status:     "429 Too Many Requests",
			Header:     headers,
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"slow down","type":"rate_limit_error","param":null,"code":"rate_limit"}}`)),
		}, nil
	})}
	provider, selected := newHTTPTestProvider(t, "https://api.test/v1", client, nil)
	_, err := provider.Generate(context.Background(), selected, basicRequest())
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Generate() error = %v, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusTooManyRequests || apiErr.RequestID != "req-123" || apiErr.Type != "rate_limit_error" || apiErr.Code != "rate_limit" || apiErr.Message != "slow down" {
		t.Fatalf("APIError = %+v", apiErr)
	}
}

func TestAPIErrorFormattingAndFallbackBody(t *testing.T) {
	var nilError *APIError
	if got := nilError.Error(); got != "openai: API error" {
		t.Fatalf("nil APIError = %q", got)
	}
	err := (&APIError{StatusCode: http.StatusBadRequest, Message: "bad input", Code: "invalid"}).Error()
	if err != "openai: API error: HTTP 400: bad input (code: invalid)" {
		t.Fatalf("APIError.Error() = %q", err)
	}

	body := &trackingBody{Reader: strings.NewReader("gateway failed")}
	provider, selected := providerWithResponse(t, &http.Response{
		StatusCode: http.StatusBadGateway,
		Status:     "502 Bad Gateway",
		Header:     make(http.Header),
		Body:       body,
	})
	_, generateErr := provider.Generate(context.Background(), selected, basicRequest())
	var apiErr *APIError
	if !errors.As(generateErr, &apiErr) || apiErr.Message != "gateway failed" {
		t.Fatalf("Generate() error = %v", generateErr)
	}
	if !body.closed.Load() {
		t.Fatal("API error response body was not closed")
	}
}

func TestGenerateReturnsTransportAndNilContextErrors(t *testing.T) {
	want := errors.New("transport failed")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, want
	})}
	provider, err := New(Config{APIKey: "test-key", HTTPClient: client, Models: []model.Model{{ID: "gpt-test"}}})
	if err != nil {
		t.Fatal(err)
	}
	selected := provider.Models()[0]
	_, err = provider.Generate(context.Background(), selected, basicRequest())
	if !errors.Is(err, want) {
		t.Fatalf("transport error = %v, want errors.Is(%v)", err, want)
	}
	_, err = provider.Generate(nil, selected, basicRequest())
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil context error = %v", err)
	}
}

func TestGenerateRejectsModelAndCanceledContextBeforeTransport(t *testing.T) {
	var calls atomic.Int64
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("unexpected transport call")
	})}
	provider, err := New(Config{APIKey: "test-key", HTTPClient: client, Models: []model.Model{{ID: "gpt-test"}}})
	if err != nil {
		t.Fatal(err)
	}
	selected := provider.Models()[0]

	_, err = provider.Generate(context.Background(), model.Model{ID: "other", Provider: ProviderID, API: APIChatCompletions}, basicRequest())
	if !errors.Is(err, ErrInvalidModel) {
		t.Fatalf("model error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = provider.Generate(ctx, selected, basicRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("transport calls = %d, want 0", calls.Load())
	}
}

func TestGenerateClosesResponseBodies(t *testing.T) {
	t.Run("invalid content type", func(t *testing.T) {
		body := &trackingBody{Reader: strings.NewReader("not SSE")}
		provider, selected := providerWithResponse(t, &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       body,
		})
		_, err := provider.Generate(context.Background(), selected, basicRequest())
		if !errors.Is(err, ErrInvalidStream) {
			t.Fatalf("Generate() error = %v", err)
		}
		if !body.closed.Load() {
			t.Fatal("response body was not closed")
		}
	})

	t.Run("consumer stops early", func(t *testing.T) {
		body := &trackingBody{Reader: strings.NewReader("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n")}
		provider, selected := providerWithResponse(t, &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"text/event-stream"}},
			Body:       body,
		})
		stream, err := provider.Generate(context.Background(), selected, basicRequest())
		if err != nil {
			t.Fatalf("Generate() error = %v", err)
		}
		for range stream {
			break
		}
		if !body.closed.Load() {
			t.Fatal("response body was not closed after early stop")
		}
	})
}

func TestProviderSupportsConcurrentGenerate(t *testing.T) {
	const generations = 32
	var calls atomic.Int64
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\n" +
					"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
					"data: [DONE]\n\n",
			)),
		}, nil
	})}
	provider, err := New(Config{APIKey: "test-key", HTTPClient: client, Models: []model.Model{{ID: "gpt-test"}}})
	if err != nil {
		t.Fatal(err)
	}
	selected := provider.Models()[0]
	llm := mustBind(t, provider, selected)

	errorsFound := make(chan error, generations)
	var wait sync.WaitGroup
	for range generations {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := model.Complete(context.Background(), llm, basicRequest())
			if err != nil {
				errorsFound <- err
				return
			}
			if len(result.Message.Content) != 1 || result.Message.Content[0].Text != "ok" {
				errorsFound <- errors.New("unexpected result")
			}
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("concurrent generation: %v", err)
	}
	if got := calls.Load(); got != generations {
		t.Fatalf("transport calls = %d, want %d", got, generations)
	}
}

func newHTTPTestProvider(t *testing.T, baseURL string, client *http.Client, headers http.Header) (*Provider, model.Model) {
	t.Helper()
	provider, err := New(Config{
		APIKey:     "test-key",
		BaseURL:    baseURL,
		HTTPClient: client,
		Headers:    headers,
		Models:     []model.Model{{ID: "gpt-test"}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return provider, provider.Models()[0]
}

func providerWithResponse(t *testing.T, response *http.Response) (*Provider, model.Model) {
	t.Helper()
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response, nil
	})}
	provider, err := New(Config{
		APIKey:     "test-key",
		HTTPClient: client,
		Models:     []model.Model{{ID: "gpt-test"}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return provider, provider.Models()[0]
}

func mustBind(t *testing.T, provider *Provider, selected model.Model) model.LLM {
	t.Helper()
	llm, err := model.Bind(provider, selected)
	if err != nil {
		t.Fatalf("model.Bind() error = %v", err)
	}
	return llm
}

func basicRequest() model.Request {
	return model.Request{Context: model.Context{Messages: []model.Message{{
		Role:    model.RoleUser,
		Content: []model.ContentBlock{{Kind: model.ContentText, Text: "hello"}},
	}}}}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type trackingBody struct {
	io.Reader
	closed atomic.Bool
}

func (b *trackingBody) Close() error {
	b.closed.Store(true)
	return nil
}
