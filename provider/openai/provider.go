// Package openai adapts OpenAI's Chat Completions API to the model.Provider
// contract.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/JIAOZAI1/acore/model"
)

const (
	defaultBaseURL = "https://api.openai.com/v1"
	chatPath       = "chat/completions"

	// ProviderID is the stable identifier returned by Provider.ID.
	ProviderID = "openai"
	// APIChatCompletions identifies the OpenAI Chat Completions protocol.
	APIChatCompletions = "openai-chat-completions"
)

// Config configures an OpenAI Provider. New snapshots Headers and Models;
// callers remain responsible for safe concurrent use of HTTPClient.
type Config struct {
	// APIKey authenticates requests and is required.
	APIKey string
	// BaseURL defaults to the public OpenAI v1 API endpoint.
	BaseURL string
	// HTTPClient defaults to http.DefaultClient. Configure timeouts on the
	// client or generation context.
	HTTPClient *http.Client
	// Headers contains optional gateway or project headers. Authorization,
	// Content-Type, and Accept are always controlled by Provider.
	Headers http.Header
	// Models is the explicit model catalog exposed by Provider.
	Models []model.Model
}

// Provider is an immutable OpenAI Chat Completions provider. It is safe for
// concurrent generation when its HTTP client and transport are also safe.
type Provider struct {
	apiKey    string
	endpoint  string
	client    *http.Client
	headers   http.Header
	models    []model.Model
	modelByID map[string]model.Model
}

// New validates config and constructs an immutable Provider.
func New(config Config) (*Provider, error) {
	apiKey := strings.TrimSpace(config.APIKey)
	if apiKey == "" {
		return nil, ErrMissingAPIKey
	}

	endpoint, err := buildEndpoint(config.BaseURL)
	if err != nil {
		return nil, err
	}
	if len(config.Models) == 0 {
		return nil, ErrNoModels
	}

	models := make([]model.Model, len(config.Models))
	modelByID := make(map[string]model.Model, len(config.Models))
	for i, candidate := range config.Models {
		candidate, err = normalizeModel(candidate)
		if err != nil {
			return nil, fmt.Errorf("openai: configure model %d: %w", i, err)
		}
		if _, exists := modelByID[candidate.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate ID %q", ErrInvalidModel, candidate.ID)
		}
		models[i] = candidate
		modelByID[candidate.ID] = candidate
	}

	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	headers := config.Headers.Clone()
	if headers == nil {
		headers = make(http.Header)
	}

	return &Provider{
		apiKey:    apiKey,
		endpoint:  endpoint,
		client:    client,
		headers:   headers,
		models:    models,
		modelByID: modelByID,
	}, nil
}

// ID returns ProviderID.
func (p *Provider) ID() string {
	return ProviderID
}

// Models returns a deep copy of the configured model descriptors.
func (p *Provider) Models() []model.Model {
	models := make([]model.Model, len(p.models))
	for i, candidate := range p.models {
		models[i] = cloneModel(candidate)
	}
	return models
}

// Generate establishes one streaming Chat Completions request. Failures after
// a response stream is established are yielded by the returned model.Stream.
func (p *Provider) Generate(ctx context.Context, selected model.Model, request model.Request) (model.Stream, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: nil context", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.validateModel(selected); err != nil {
		return nil, err
	}

	body, err := buildRequest(selected, request)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("%w: encode request: %v", ErrInvalidRequest, err)
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("openai: create request: %w", err)
	}
	httpRequest.Header = p.headers.Clone()
	httpRequest.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream")

	response, err := p.client.Do(httpRequest)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("openai: send request: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		defer response.Body.Close()
		return nil, readAPIError(response)
	}
	if err := validateStreamResponse(response); err != nil {
		response.Body.Close()
		return nil, err
	}

	return func(yield func(model.Event, error) bool) {
		defer response.Body.Close()
		consumeStream(ctx, response.Body, selected, yield)
	}, nil
}

func buildEndpoint(baseURL string) (string, error) {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("openai: parse base URL: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", fmt.Errorf("openai: base URL must be an absolute HTTP URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("openai: base URL must not contain user info, query, or fragment")
	}
	return parsed.JoinPath(chatPath).String(), nil
}

func normalizeModel(candidate model.Model) (model.Model, error) {
	if strings.TrimSpace(candidate.ID) == "" {
		return model.Model{}, fmt.Errorf("%w: empty ID", ErrInvalidModel)
	}
	if candidate.Provider == "" {
		candidate.Provider = ProviderID
	}
	if candidate.Provider != ProviderID {
		return model.Model{}, fmt.Errorf("%w: model %q belongs to provider %q", ErrInvalidModel, candidate.ID, candidate.Provider)
	}
	if candidate.API == "" {
		candidate.API = APIChatCompletions
	}
	if candidate.API != APIChatCompletions {
		return model.Model{}, fmt.Errorf("%w: model %q uses API %q", ErrInvalidModel, candidate.ID, candidate.API)
	}
	return cloneModel(candidate), nil
}

func cloneModel(candidate model.Model) model.Model {
	candidate.InputModalities = slices.Clone(candidate.InputModalities)
	return candidate
}

func (p *Provider) validateModel(selected model.Model) error {
	if strings.TrimSpace(selected.ID) == "" {
		return fmt.Errorf("%w: empty ID", ErrInvalidModel)
	}
	if _, exists := p.modelByID[selected.ID]; !exists {
		return fmt.Errorf("%w: model %q is not configured", ErrInvalidModel, selected.ID)
	}
	if selected.Provider != ProviderID {
		return fmt.Errorf("%w: model %q belongs to provider %q", ErrInvalidModel, selected.ID, selected.Provider)
	}
	if selected.API != APIChatCompletions {
		return fmt.Errorf("%w: model %q uses API %q", ErrInvalidModel, selected.ID, selected.API)
	}
	return nil
}

func validateStreamResponse(response *http.Response) error {
	contentType := response.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "text/event-stream") {
		return fmt.Errorf("%w: unexpected Content-Type %q", ErrInvalidStream, contentType)
	}
	return nil
}

var _ model.Provider = (*Provider)(nil)
