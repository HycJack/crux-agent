package core

import (
	"net/http"
	"testing"
)

func TestHeadersToRecord(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer abc")
	h.Add("X-Multi", "v1")
	h.Add("X-Multi", "v2")
	got := HeadersToRecord(h)
	if got["Authorization"] != "Bearer abc" {
		t.Errorf("Authorization: got %q", got["Authorization"])
	}
	if got["X-Multi"] != "v1, v2" {
		t.Errorf("X-Multi join: got %q", got["X-Multi"])
	}
	if HeadersToRecord(nil) != nil {
		t.Errorf("nil header should return nil")
	}
	if HeadersToRecord(http.Header{}) != nil {
		t.Errorf("empty header should return nil")
	}
}

func TestProviderHeadersToRecord(t *testing.T) {
	if got := ProviderHeadersToRecord(nil); got != nil {
		t.Errorf("nil -> nil, got %v", got)
	}
	if got := ProviderHeadersToRecord(ProviderHeaders{}); got != nil {
		t.Errorf("empty -> nil, got %v", got)
	}
	got := ProviderHeadersToRecord(ProviderHeaders{"A": "1", "B": ""})
	if got == nil || got["A"] != "1" || len(got) != 1 {
		t.Errorf("empty-valued keys should be skipped, got %v", got)
	}
}

func TestMergeProviderHeaders(t *testing.T) {
	defaults := ProviderHeaders{"X-Auth": "default", "X-Model": "default-model"}
	caller := ProviderHeaders{"X-Auth": "caller", "X-Custom": "x"}

	merged := MergeProviderHeaders(defaults, caller)
	if merged["X-Auth"] != "caller" {
		t.Errorf("caller should override default: got %q", merged["X-Auth"])
	}
	if merged["X-Model"] != "default-model" {
		t.Errorf("default preserved: got %q", merged["X-Model"])
	}
	if merged["X-Custom"] != "x" {
		t.Errorf("caller addition: got %q", merged["X-Custom"])
	}

	// Nil caller value suppresses default.
	merged2 := MergeProviderHeaders(defaults, ProviderHeaders{"X-Auth": ""})
	if _, ok := merged2["X-Auth"]; ok {
		t.Errorf("nil caller should suppress default")
	}
}

func TestMergeProviderHeaders_BothNil(t *testing.T) {
	if got := MergeProviderHeaders(nil, nil); got != nil {
		t.Errorf("nil+nil should be nil, got %v", got)
	}
}