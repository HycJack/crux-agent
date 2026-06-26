package oauth

import (
	"testing"
)

func TestCredentialsFields(t *testing.T) {
	creds := Credentials{
		Access:  "access-token",
		Refresh: "refresh-token",
		Expires: 1234567890,
		Extra:   map[string]any{"key": "value"},
	}

	if creds.Access != "access-token" {
		t.Errorf("expected access 'access-token', got '%s'", creds.Access)
	}
	if creds.Refresh != "refresh-token" {
		t.Errorf("expected refresh 'refresh-token', got '%s'", creds.Refresh)
	}
	if creds.Expires != 1234567890 {
		t.Errorf("expected expires 1234567890, got %d", creds.Expires)
	}
	if creds.Extra["key"] != "value" {
		t.Errorf("expected extra key=value, got %v", creds.Extra["key"])
	}
}

func TestProviderInterfaceFields(t *testing.T) {
	p := ProviderInterface{
		ID:   "test",
		Name: "Test Provider",
	}

	if p.ID != "test" {
		t.Errorf("expected ID 'test', got '%s'", p.ID)
	}
	if p.Name != "Test Provider" {
		t.Errorf("expected Name 'Test Provider', got '%s'", p.Name)
	}
}

func TestLoginCallbacksFields(t *testing.T) {
	var authURL string
	var deviceCode string

	callbacks := LoginCallbacks{
		OnAuth: func(url string) {
			authURL = url
		},
		OnDeviceCode: func(code, uri string) {
			deviceCode = code
		},
		OnPrompt: func(msg string) {},
	}

	callbacks.OnAuth("https://example.com/auth")
	if authURL != "https://example.com/auth" {
		t.Errorf("expected auth URL, got '%s'", authURL)
	}

	callbacks.OnDeviceCode("ABC-123", "https://example.com/device")
	if deviceCode != "ABC-123" {
		t.Errorf("expected device code 'ABC-123', got '%s'", deviceCode)
	}
}
