package oauth

import (
	"testing"
)

func TestDeviceCodePollOptionsDefaults(t *testing.T) {
	opts := DeviceCodePollOptions{
		TokenURL:   "https://example.com/token",
		ClientID:   "test-client",
		DeviceCode: "test-code",
	}

	if opts.TokenURL != "https://example.com/token" {
		t.Errorf("expected token URL, got %s", opts.TokenURL)
	}
	if opts.ClientID != "test-client" {
		t.Errorf("expected client ID, got %s", opts.ClientID)
	}
	if opts.DeviceCode != "test-code" {
		t.Errorf("expected device code, got %s", opts.DeviceCode)
	}
}

func TestDeviceCodeResponse(t *testing.T) {
	resp := DeviceCodeResponse{
		DeviceCode:      "abc123",
		UserCode:        "XYZ-123",
		VerificationURI: "https://example.com/device",
		ExpiresIn:       600,
		Interval:        5,
	}

	if resp.DeviceCode != "abc123" {
		t.Errorf("expected device code 'abc123', got '%s'", resp.DeviceCode)
	}
	if resp.UserCode != "XYZ-123" {
		t.Errorf("expected user code 'XYZ-123', got '%s'", resp.UserCode)
	}
}

func TestDeviceCodeTokenResponse(t *testing.T) {
	resp := DeviceCodeTokenResponse{
		AccessToken:  "token123",
		RefreshToken: "refresh456",
		TokenType:    "bearer",
		ExpiresIn:    3600,
	}

	if resp.AccessToken != "token123" {
		t.Errorf("expected access token 'token123', got '%s'", resp.AccessToken)
	}
	if resp.RefreshToken != "refresh456" {
		t.Errorf("expected refresh token 'refresh456', got '%s'", resp.RefreshToken)
	}
}

func TestDeviceCodeTokenResponseError(t *testing.T) {
	resp := DeviceCodeTokenResponse{
		Error:     "authorization_pending",
		ErrorDesc: "User has not yet authorized",
	}

	if resp.Error != "authorization_pending" {
		t.Errorf("expected error 'authorization_pending', got '%s'", resp.Error)
	}
}
