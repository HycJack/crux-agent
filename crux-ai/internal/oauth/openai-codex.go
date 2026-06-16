/*
 * 功能说明：OpenAI Codex OAuth 认证实现
 * 
 * 解决的问题：
 * 1. 需要实现 OpenAI Codex 的 OAuth 认证流程
 * 2. 需要支持 PKCE 安全认证
 * 3. 需要从 JWT 中提取账户 ID
 * 4. 需要支持令牌刷新
 * 
 * 解决方案：
 * 1. 使用授权码流程 + PKCE 进行认证
 * 2. 启动本地 HTTP 服务器接收回调
 * 3. 解析 JWT 获取账户 ID
 * 4. 实现令牌刷新逻辑
 * 
 * 应用场景：
 * - 为 OpenAI Codex API 获取访问令牌
 * - CLI 工具的 OAuth 认证
 */
package oauth

import (
	"context"
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
	codexAuthURL  = "https://auth0.openai.com/authorize" // 认证端点
	codexTokenURL = "https://auth0.openai.com/oauth/token" // 令牌端点
	codexPort     = 1455                                  // 本地回调端口
	codexClientID = "DRivsnmYLKgM1v3oVGGcN9rgCoGWxjCs"    // 客户端 ID
)

// loginOpenAICodex performs OAuth login with OpenAI Codex using PKCE flow.
// || 使用 PKCE 流程进行 OpenAI Codex OAuth 登录
// 参数：
//   ctx - 上下文
//   callbacks - 登录回调函数
// 返回：
//   凭证信息和错误
func loginOpenAICodex(ctx context.Context, callbacks LoginCallbacks) (Credentials, error) {
	// Generate PKCE challenge
	// || 生成 PKCE challenge
	pkce, err := GeneratePKCE()
	if err != nil {
		return Credentials{}, fmt.Errorf("failed to generate PKCE: %w", err)
	}

	// Build redirect URI
	// || 构建重定向 URI
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", codexPort)

	// Generate state for CSRF protection
	// || 生成 state 用于 CSRF 保护
	state, err := generateState()
	if err != nil {
		return Credentials{}, fmt.Errorf("failed to generate state: %w", err)
	}

	// Build authorization URL
	// || 构建授权 URL
	authURL := fmt.Sprintf("%s?response_type=code&client_id=%s&redirect_uri=%s&scope=%s&state=%s&code_challenge=%s&code_challenge_method=S256&audience=https://api.openai.com/v1&prompt=login",
		codexAuthURL,
		url.QueryEscape(codexClientID),
		url.QueryEscape(redirectURI),
		url.QueryEscape("openid profile email"),
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
		w.Write([]byte(oauthSuccessHTML("OpenAI Codex")))
		codeCh <- code
	})

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", codexPort),
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
		return exchangeCodexCode(ctx, code, pkce.Verifier, redirectURI)
	}
}

// exchangeCodexCode exchanges the authorization code for tokens.
// || 交换授权码获取令牌
// 参数：
//   ctx - 上下文
//   code - 授权码
//   verifier - PKCE verifier
//   redirectURI - 重定向 URI
// 返回：
//   凭证信息和错误
func exchangeCodexCode(ctx context.Context, code, verifier, redirectURI string) (Credentials, error) {
	client := &http.Client{Timeout: 30 * time.Second}

	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {codexClientID},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", codexTokenURL, strings.NewReader(data.Encode()))
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
		IDToken      string `json:"id_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return Credentials{}, err
	}

	creds := Credentials{
		Access:  tokenResp.AccessToken,
		Refresh: tokenResp.RefreshToken,
		Extra:   make(map[string]any),
	}
	if tokenResp.ExpiresIn > 0 {
		creds.Expires = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Unix()
	}

	// Extract account ID from JWT
	// || 从 JWT 中提取账户 ID
	if tokenResp.IDToken != "" {
		if accountID := extractJWTAccountID(tokenResp.IDToken); accountID != "" {
			creds.Extra["accountId"] = accountID
		}
	}

	return creds, nil
}

// refreshOpenAICodex refreshes the OpenAI Codex access token.
// || 刷新 OpenAI Codex 访问令牌
// 参数：
//   ctx - 上下文
//   credentials - 当前凭证
// 返回：
//   更新后的凭证和错误
func refreshOpenAICodex(ctx context.Context, credentials Credentials) (Credentials, error) {
	if credentials.Refresh == "" {
		return credentials, fmt.Errorf("no refresh token available")
	}

	client := &http.Client{Timeout: 30 * time.Second}

	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {credentials.Refresh},
		"client_id":     {codexClientID},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", codexTokenURL, strings.NewReader(data.Encode()))
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
		IDToken      string `json:"id_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return credentials, err
	}

	creds := Credentials{
		Access:  tokenResp.AccessToken,
		Refresh: tokenResp.RefreshToken,
		Extra:   credentials.Extra,
	}
	if tokenResp.ExpiresIn > 0 {
		creds.Expires = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Unix()
	}

	// Extract account ID from JWT if present
	// || 如果存在 ID Token，从中提取账户 ID
	if tokenResp.IDToken != "" {
		if accountID := extractJWTAccountID(tokenResp.IDToken); accountID != "" {
			if creds.Extra == nil {
				creds.Extra = make(map[string]any)
			}
			creds.Extra["accountId"] = accountID
		}
	}

	return creds, nil
}

// extractJWTAccountID extracts the account ID from a JWT token.
// || 从 JWT 令牌中提取账户 ID
// 参数：
//   token - JWT 令牌
// 返回：
//   账户 ID（如果解析失败返回空字符串）
func extractJWTAccountID(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}

	// Decode base64 URL encoded payload
	// || 解码 base64 URL 编码的 payload
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}

	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}

	// Extract subject claim as account ID
	// || 提取 subject 声明作为账户 ID
	if sub, ok := claims["sub"].(string); ok {
		return sub
	}
	return ""
}
