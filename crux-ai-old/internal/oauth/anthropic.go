/*
 * 功能说明：Anthropic OAuth 认证实现
 *
 * 解决的问题：
 * 1. 需要实现 Anthropic API 的 OAuth 认证流程
 * 2. 需要支持 PKCE 安全认证
 * 3. 需要本地 HTTP 服务器处理回调
 * 4. 需要支持令牌刷新
 *
 * 解决方案：
 * 1. 使用授权码流程 + PKCE 进行认证
 * 2. 启动本地 HTTP 服务器接收回调
 * 3. 实现代码交换和令牌刷新逻辑
 *
 * 应用场景：
 * - 为 Anthropic Claude API 获取访问令牌
 * - 支持 CLI 工具的 OAuth 认证
 */
package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	anthropicAuthURL  = "https://console.anthropic.com/oauth/authorize" // 认证端点
	anthropicTokenURL = "https://console.anthropic.com/oauth/token"     // 令牌端点
	anthropicPort     = 53692                                           // 本地回调端口
	// Client ID is base64 encoded
	anthropicClientIDB64 = "ZGNfZWY0OGNhMzktNjZhYi00YTIwLWFhYjktOTMxYmI0ZGI1ZTY5" // base64 编码的客户端 ID
)

// getAnthropicClientID decodes the base64-encoded client ID.
// || 解码 base64 编码的客户端 ID
func getAnthropicClientID() string {
	b, _ := base64.StdEncoding.DecodeString(anthropicClientIDB64)
	return string(b)
}

// loginAnthropic performs OAuth login with Anthropic using PKCE flow.
// || 使用 PKCE 流程进行 Anthropic OAuth 登录
// 参数：
//
//	ctx - 上下文
//	callbacks - 登录回调函数
//
// 返回：
//
//	凭证信息和错误
func loginAnthropic(ctx context.Context, callbacks LoginCallbacks) (Credentials, error) {
	// Generate PKCE challenge
	// || 生成 PKCE challenge
	pkce, err := GeneratePKCE()
	if err != nil {
		return Credentials{}, fmt.Errorf("failed to generate PKCE: %w", err)
	}

	// Build redirect URI
	// || 构建重定向 URI
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", anthropicPort)

	// Generate state for CSRF protection
	// || 生成 state 用于 CSRF 保护
	state, err := generateState()
	if err != nil {
		return Credentials{}, fmt.Errorf("failed to generate state: %w", err)
	}

	// Build authorization URL
	// || 构建授权 URL
	authURL := fmt.Sprintf("%s?response_type=code&client_id=%s&redirect_uri=%s&scope=%s&state=%s&code_challenge=%s&code_challenge_method=S256",
		anthropicAuthURL,
		url.QueryEscape(getAnthropicClientID()),
		url.QueryEscape(redirectURI),
		url.QueryEscape("org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"),
		url.QueryEscape(state),
		url.QueryEscape(pkce.Challenge),
	)

	// Notify caller about the auth URL
	// || 通知调用者授权 URL
	if callbacks.OnAuth != nil {
		callbacks.OnAuth(authURL)
	}

	// Create channels for result
	// || 创建结果通道
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	// Set up HTTP server for callback
	// || 设置回调 HTTP 服务器
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			errCh <- fmt.Errorf("no authorization code received")
			http.Error(w, "No code", http.StatusBadRequest)
			return
		}

		// Validate state
		// || 验证 state
		if r.URL.Query().Get("state") != state {
			errCh <- fmt.Errorf("state mismatch")
			http.Error(w, "State mismatch", http.StatusBadRequest)
			return
		}

		// Show success page
		// || 显示成功页面
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(oauthSuccessHTML("Anthropic")))
		codeCh <- code
	})

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", anthropicPort),
		Handler: mux,
		BaseContext: func(l net.Listener) context.Context {
			return ctx
		},
	}

	// Start server in background
	// || 在后台启动服务器
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			select {
			case errCh <- err:
			default:
			}
		}
	}()

	// Ensure server shutdown
	// || 确保服务器关闭
	defer server.Shutdown(context.Background())

	// Wait for result
	// || 等待结果
	select {
	case <-ctx.Done():
		return Credentials{}, ctx.Err()
	case err := <-errCh:
		return Credentials{}, err
	case code := <-codeCh:
		return exchangeAnthropicCode(ctx, code, pkce.Verifier, redirectURI)
	}
}

// exchangeAnthropicCode exchanges the authorization code for tokens.
// || 交换授权码获取令牌
// 参数：
//
//	ctx - 上下文
//	code - 授权码
//	verifier - PKCE verifier
//	redirectURI - 重定向 URI
//
// 返回：
//
//	凭证信息和错误
func exchangeAnthropicCode(ctx context.Context, code, verifier, redirectURI string) (Credentials, error) {
	client := &http.Client{Timeout: 30 * time.Second}

	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {getAnthropicClientID()},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", anthropicTokenURL, strings.NewReader(data.Encode()))
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

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return Credentials{}, err
	}

	creds := Credentials{
		Access:  tokenResp.AccessToken,
		Refresh: tokenResp.RefreshToken,
	}
	if tokenResp.ExpiresIn > 0 {
		creds.Expires = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Unix()
	}
	return creds, nil
}

// refreshAnthropic refreshes the Anthropic access token.
// || 刷新 Anthropic 访问令牌
// 参数：
//
//	ctx - 上下文
//	credentials - 当前凭证
//
// 返回：
//
//	更新后的凭证和错误
func refreshAnthropic(ctx context.Context, credentials Credentials) (Credentials, error) {
	if credentials.Refresh == "" {
		return credentials, fmt.Errorf("no refresh token available")
	}

	client := &http.Client{Timeout: 30 * time.Second}

	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {credentials.Refresh},
		"client_id":     {getAnthropicClientID()},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", anthropicTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return credentials, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return credentials, err
	}
	defer resp.Body.Close()

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return credentials, err
	}

	creds := Credentials{
		Access:  tokenResp.AccessToken,
		Refresh: tokenResp.RefreshToken,
	}
	if tokenResp.ExpiresIn > 0 {
		creds.Expires = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Unix()
	}
	return creds, nil
}

// generateState generates a random state string for CSRF protection.
// || 生成随机 state 字符串用于 CSRF 保护
func generateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// oauthSuccessHTML returns a success page HTML.
// || 返回成功页面 HTML
func oauthSuccessHTML(provider string) string {
	return `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Login Successful</title>
<style>body{background:#1a1a2e;color:#e0e0e0;font-family:system-ui;display:flex;justify-content:center;align-items:center;height:100vh;margin:0}
.container{text-align:center}.check{font-size:64px;color:#4ade80}</style></head>
<body><div class="container"><div class="check">✓</div>
<h1>Login Successful</h1><p>You may close this window.</p></div></body></html>`
}
