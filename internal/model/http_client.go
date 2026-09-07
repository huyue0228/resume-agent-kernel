package model

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"resume-agent-kernel/internal/protocol"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Usage struct {
	InputTokens  int
	OutputTokens int
}

type Client interface {
	Complete(context.Context, []Message) (string, Usage, error)
}

type HTTPClient struct {
	OutputBudget int
	config       protocol.ModelConfig
	apiKey       string
	client       *http.Client
}

func NewHTTPClient(config protocol.ModelConfig, apiKey string) (*HTTPClient, error) {
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("invalid model base URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("model base URL must use http or https")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: config.InsecureSkipVerify} //nolint:gosec -- explicit intranet compatibility setting
	return &HTTPClient{
		config: config,
		apiKey: apiKey,
		client: &http.Client{Transport: transport, Timeout: time.Duration(config.TimeoutSeconds * float64(time.Second))},
	}, nil
}

func (c *HTTPClient) Complete(ctx context.Context, messages []Message) (string, Usage, error) {
	attempts := c.config.RetryCount + 1
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		content, usage, retryable, err := c.completeOnce(ctx, messages)
		if err == nil {
			return content, usage, nil
		}
		lastErr = err
		if !retryable || attempt+1 >= attempts {
			break
		}
		delay := time.Duration(1<<attempt) * 250 * time.Millisecond
		select {
		case <-ctx.Done():
			return "", Usage{}, ctx.Err()
		case <-time.After(delay):
		}
	}
	return "", Usage{}, lastErr
}

func (c *HTTPClient) completeOnce(ctx context.Context, messages []Message) (string, Usage, bool, error) {
	endpoint := strings.TrimRight(c.config.BaseURL, "/")
	var body map[string]any
	if c.config.APIStyle == "responses" {
		endpoint += "/responses"
		body = map[string]any{"model": c.config.ModelName, "input": messages, "store": false}
	} else {
		endpoint += "/chat/completions"
		body = map[string]any{"model": c.config.ModelName, "messages": messages, "response_format": map[string]string{"type": "json_object"}, "stream": false}
	}
	payload, err := json.Marshal(body)
	if c.OutputBudget > 0 {
		if c.config.APIStyle == "responses" {
			body["max_output_tokens"] = c.OutputBudget
		} else {
			body["max_completion_tokens"] = c.OutputBudget
		}
		payload, err = json.Marshal(body)
	}
	if err != nil {
		return "", Usage{}, false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", Usage{}, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return "", Usage{}, true, errors.New("model connection failed")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", Usage{}, true, errors.New("model response read failed")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return "", Usage{}, retryable, fmt.Errorf("model request failed with status %d", resp.StatusCode)
	}
	if c.config.APIStyle == "responses" {
		content, usage, err := parseResponses(data)
		return content, usage, false, err
	}
	content, usage, err := parseChat(data)
	return content, usage, false, err
}

func parseChat(data []byte) (string, Usage, error) {
	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &response); err != nil || len(response.Choices) == 0 || strings.TrimSpace(response.Choices[0].Message.Content) == "" {
		return "", Usage{}, errors.New("model returned an invalid chat response")
	}
	return response.Choices[0].Message.Content, Usage{InputTokens: response.Usage.PromptTokens, OutputTokens: response.Usage.CompletionTokens}, nil
}

func parseResponses(data []byte) (string, Usage, error) {
	var response struct {
		OutputText string `json:"output_text"`
		Output     []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return "", Usage{}, errors.New("model returned an invalid responses payload")
	}
	content := response.OutputText
	if strings.TrimSpace(content) == "" {
		for _, item := range response.Output {
			for _, part := range item.Content {
				if part.Type == "output_text" || part.Type == "text" {
					content += part.Text
				}
			}
		}
	}
	if strings.TrimSpace(content) == "" {
		return "", Usage{}, errors.New("model returned no output text")
	}
	return content, Usage{InputTokens: response.Usage.InputTokens, OutputTokens: response.Usage.OutputTokens}, nil
}
