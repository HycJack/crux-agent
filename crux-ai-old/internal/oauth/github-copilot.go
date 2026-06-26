/*
 * 功能说明：GitHub Copilot OAuth 认证实现
 * 
 * 解决的问题：
 * 1. 需要实现 GitHub Copilot 的设备码认证流程
 * 2. 需要将 GitHub 令牌转换为 Copilot 令牌
 * 3. 需要支持令牌刷新
 * 
 * 解决方案：
 * 1. 使用 RFC 8628 设备码流程获取 GitHub 令牌
 * 2. 使用 GitHub 令牌换取 Copilot 专用令牌
 * 3. 实现令牌刷新逻辑
 * 
 * 应用场景：
 * - 为 GitHub Copilot 获取访问令牌
 * - CLI 工具的无浏览器认证
 */
package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	githubDeviceCodeURL = "https://github.com/login/device/code"    // GitHub 设备码端点
	githubTokenURL      = "https://github.com/login/oauth/access_token" // GitHub 令牌端点
	copilotTokenURL     = "https://api.github.com/copilot_internal/v2/token" // Copilot 令牌端点
	githubClientID      = "Iv1.b507a08c87ecfe98"                    // GitHub 客户端 ID
)

// loginGitHubCopilot performs OAuth login with GitHub using device code flow.
// || 使用设备码流程进行 GitHub Copilot OAuth 登录
// 参数：
//   ctx - 上下文
//   callbacks - 登录回调函数
// 返回：
//   凭证信息和错误
func loginGitHubCopilot(ctx context.Context, callbacks LoginCallbacks) (Credentials, error) {
	client := &http.Client{Timeout: 30 * time.Second}

	// Request device code from GitHub
	// || 从 GitHub 请求设备码
	data := url.Values{
		"client_id": {githubClientID},
		"scope":     {"read:user"},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", githubDeviceCodeURL, strings.NewReader(data.Encode()))
	if err != nil {
		return Credentials{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return Credentials{}, err
	}
	defer resp.Body.Close()

	var deviceResp DeviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&deviceResp); err != nil {
		return Credentials{}, err
	}

	// Notify caller about device code
	// || 通知调用者设备码信息
	if callbacks.OnDeviceCode != nil {
		callbacks.OnDeviceCode(deviceResp.UserCode, deviceResp.VerificationURI)
	}

	if callbacks.OnPrompt != nil {
		callbacks.OnPrompt(fmt.Sprintf("Go to %s and enter code: %s", deviceResp.VerificationURI, deviceResp.UserCode))
	}

	// Poll for token using device code
	// || 使用设备码轮询获取令牌
	creds, err := PollDeviceCodeFlow(ctx, DeviceCodePollOptions{
		TokenURL:   githubTokenURL,
		ClientID:   githubClientID,
		DeviceCode: deviceResp.DeviceCode,
		Interval:   deviceResp.Interval,
		ExpiresIn:  deviceResp.ExpiresIn,
		OnProgress: callbacks.OnProgress,
	})
	if err != nil {
		return Credentials{}, err
	}

	// Exchange GitHub token for Copilot token
	// || 使用 GitHub 令牌换取 Copilot 令牌
	copilotToken, err := getCopilotToken(ctx, creds.Access)
	if err != nil {
		return Credentials{}, err
	}

	creds.Access = copilotToken
	return creds, nil
}

// getCopilotToken exchanges a GitHub token for a Copilot token.
// || 使用 GitHub 令牌换取 Copilot 令牌
// 参数：
//   ctx - 上下文
//   githubToken - GitHub 访问令牌
// 返回：
//   Copilot 令牌和错误
func getCopilotToken(ctx context.Context, githubToken string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}

	req, err := http.NewRequestWithContext(ctx, "GET", copilotTokenURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "token "+githubToken)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var tokenResp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", err
	}

	return tokenResp.Token, nil
}

// refreshGitHubCopilot refreshes the GitHub Copilot token.
// || 刷新 GitHub Copilot 令牌
// 参数：
//   ctx - 上下文
//   credentials - 当前凭证
// 返回：
//   更新后的凭证和错误
func refreshGitHubCopilot(ctx context.Context, credentials Credentials) (Credentials, error) {
	// GitHub Copilot tokens are short-lived, need to re-fetch from the GitHub token
	// || GitHub Copilot 令牌有效期短，需要从 GitHub 令牌重新获取
	if credentials.Refresh == "" {
		return credentials, fmt.Errorf("no refresh token available")
	}

	// Use the refresh token (which is the GitHub token) to get a new Copilot token
	// || 使用刷新令牌（即 GitHub 令牌）获取新的 Copilot 令牌
	copilotToken, err := getCopilotToken(ctx, credentials.Refresh)
	if err != nil {
		return credentials, err
	}

	credentials.Access = copilotToken
	credentials.Expires = time.Now().Add(30 * time.Minute).Unix()
	return credentials, nil
}
