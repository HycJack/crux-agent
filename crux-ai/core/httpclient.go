package core

import (
	"bytes"
	"context"
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
