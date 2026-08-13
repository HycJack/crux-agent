package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

var (
	SSEClient = &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			ResponseHeaderTimeout: 2 * time.Minute,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
		},
	}

	RegularClient = &http.Client{
		Timeout: 30 * time.Second,
	}
)

// NewProviderRequest builds an HTTP request with the standard provider
// headers: Content-Type, Authorization, model.Headers, and opts.Headers.
// It also returns the resolved API key so callers can log or validate it.
//
// The caller is responsible for choosing the HTTP client (SSEClient,
// NewTimeoutClient, etc.) and executing the request.
func NewProviderRequest(ctx context.Context, method, url string, body []byte, provider KnownProvider, model Model, opts StreamOptions) (*http.Request, string, error) {
	apiKey := ResolveAPIKey(provider, opts.APIKey)
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	for k, v := range model.Headers {
		req.Header.Set(k, v)
	}
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}
	return req, apiKey, nil
}

// HTTPResponse represents an HTTP response for retry purposes.
type HTTPResponse struct {
	StatusCode int
	Body       []byte
}

// DoWithRetry executes an HTTP request with automatic retry on transient
// errors (5xx, 429, network errors). Returns the response on success.
// The caller must close resp.Body.
//
// method/url/body/headers are passed individually (not via *http.Request)
// so the body bytes can be re-read on retry.
// Use this instead of raw client.Do() in all provider implementations.
func DoWithRetry(ctx context.Context, client *http.Client, method, url string, body []byte, headers map[string]string, provider KnownProvider, opts StreamOptions) (*http.Response, error) {
	retryCfg := RetryConfig{
		Enabled:    true,
		MaxRetries: opts.MaxRetries,
		BaseDelay:  DefaultBaseDelay,
		MaxDelay:   DefaultMaxDelay,
		Multiplier: DefaultBackoffMultiplier,
	}
	if retryCfg.MaxRetries <= 0 {
		retryCfg.MaxRetries = DefaultMaxRetries
	}

	var resp *http.Response
	err := Retry(ctx, retryCfg, func() error {
		req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err = client.Do(req)
		if err != nil {
			return WrapHTTPTimeout(provider, time.Duration(opts.TimeoutMs)*time.Millisecond, err)
		}

		// Check for HTTP-level errors that should be retried
		if resp.StatusCode >= 400 {
			bodyBytes, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				return fmt.Errorf("%s: failed to read error body: %w", provider, readErr)
			}

			bodyStr := string(bodyBytes)

			// Classify first
			if classified := ClassifyHTTPError(provider, resp.StatusCode, bodyStr); classified != nil {
				// Return typed error so Retry can check IsRetryableError
				return classified
			}

			// Fallback to raw error for unmatched codes
			return fmt.Errorf("%s: API error %d: %s", provider, resp.StatusCode, bodyStr)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}
