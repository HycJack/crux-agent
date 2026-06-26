package core

import (
	"net/http"
	"strings"
)

// ProviderHeaders is the per-request header map. A nil value suppresses a
// provider/API default header with the same name.
//
// Reference: pi-mono packages/ai/src/utils/headers.ts (providerHeadersToRecord).
type ProviderHeaders = map[string]string

// HeadersToRecord converts net/http.Header to a flat string map. Header
// values are joined with ", " per RFC 7230 (which is the format Go uses
// internally as well).
func HeadersToRecord(h http.Header) map[string]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = strings.Join(v, ", ")
	}
	return out
}

// ProviderHeadersToRecord flattens a ProviderHeaders map into the format
// expected by net/http callers. nil-valued keys are skipped (used to
// suppress defaults), and a fully-empty result returns nil so callers
// can branch cleanly.
func ProviderHeadersToRecord(h ProviderHeaders) map[string]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		if v == "" {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// MergeProviderHeaders overlays caller-supplied headers onto defaults.
// Caller headers win. A nil-valued caller entry suppresses a default
// with the same key.
func MergeProviderHeaders(defaults, caller ProviderHeaders) ProviderHeaders {
	if len(defaults) == 0 && len(caller) == 0 {
		return nil
	}
	out := make(ProviderHeaders, len(defaults)+len(caller))
	for k, v := range defaults {
		out[k] = v
	}
	for k, v := range caller {
		if v == "" {
			delete(out, k)
		} else {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}