/*
 * 功能说明：OAuth 设备码认证流程实现
 *
 * 解决的问题：
 * 1. 设备无法直接输入密码时需要替代的认证方式
 * 2. 需要实现 RFC 8628 标准的设备码流程
 * 3. 需要处理轮询过程中的多种状态（pending、slow_down、expired 等）
 *
 * 解决方案：
 * 1. 实现标准的设备码轮询流程
 * 2. 支持可配置的轮询间隔和超时时间
 * 3. 提供进度回调机制
 * 4. 正确处理各种错误状态
 *
 * 应用场景：
 * - 终端设备的 OAuth 认证
 * - 无头设备的认证
 * - CLI 工具的认证
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

// DeviceCodeResponse holds the response from a device code request.
// || 存储设备码请求的响应
type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`      // 设备码
	UserCode        string `json:"user_code"`        // 用户码（用户输入）
	VerificationURI string `json:"verification_uri"` // 验证 URL
	ExpiresIn       int    `json:"expires_in"`       // 过期时间（秒）
	Interval        int    `json:"interval"`         // 轮询间隔（秒）
}

// DeviceCodeTokenResponse holds the response from polling for a token.
// || 存储轮询令牌的响应
type DeviceCodeTokenResponse struct {
	AccessToken  string `json:"access_token"`      // 访问令牌
	RefreshToken string `json:"refresh_token"`     // 刷新令牌
	TokenType    string `json:"token_type"`        // 令牌类型
	ExpiresIn    int    `json:"expires_in"`        // 过期时间（秒）
	Error        string `json:"error"`             // 错误码
	ErrorDesc    string `json:"error_description"` // 错误描述
}

// PollDeviceCodeFlow implements RFC 8628 device code flow polling.
// || 实现 RFC 8628 设备码流程的轮询
// 参数：
//
//	ctx - 上下文（支持取消）
//	opts - 轮询选项
//
// 返回：
//
//	凭证信息和错误
func PollDeviceCodeFlow(ctx context.Context, opts DeviceCodePollOptions) (Credentials, error) {
	// Set default interval if not provided
	// || 如果未提供间隔，使用默认值
	interval := opts.Interval
	if interval <= 0 {
		interval = 5
	}

	// Calculate deadline
	// || 计算截止时间
	deadline := time.Now().Add(time.Duration(opts.ExpiresIn) * time.Second)
	if opts.Deadline > 0 {
		deadline = time.Now().Add(opts.Deadline)
	}

	// Create HTTP client with timeout
	// || 创建带超时的 HTTP 客户端
	client := &http.Client{Timeout: 30 * time.Second}

	// Polling loop
	// || 轮询循环
	for {
		// Check for context cancellation
		// || 检查上下文是否取消
		select {
		case <-ctx.Done():
			return Credentials{}, ctx.Err()
		default:
		}

		// Check if deadline exceeded
		// || 检查是否超时
		if time.Now().After(deadline) {
			return Credentials{}, fmt.Errorf("device code flow expired")
		}

		// Wait for interval before next poll
		// || 等待轮询间隔
		time.Sleep(time.Duration(interval) * time.Second)

		// Build request data
		// || 构建请求数据
		data := url.Values{
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"device_code": {opts.DeviceCode},
			"client_id":   {opts.ClientID},
		}

		// Add scopes if provided
		// || 如果提供了 scopes，添加到请求
		if len(opts.Scopes) > 0 {
			data.Set("scope", strings.Join(opts.Scopes, " "))
		}

		// Create request
		// || 创建请求
		req, err := http.NewRequestWithContext(ctx, "POST", opts.TokenURL, strings.NewReader(data.Encode()))
		if err != nil {
			return Credentials{}, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")

		// Execute request
		// || 执行请求
		resp, err := client.Do(req)
		if err != nil {
			return Credentials{}, err
		}

		// Parse response
		// || 解析响应
		var tokenResp DeviceCodeTokenResponse
		if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil && resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return Credentials{}, fmt.Errorf("device code request failed with status %d", resp.StatusCode)
		}
		resp.Body.Close()

		// Handle response based on error field
		// || 根据错误字段处理响应
		switch tokenResp.Error {
		case "":
			// Success - return credentials
			// || 成功 - 返回凭证
			creds := Credentials{
				Access:  tokenResp.AccessToken,
				Refresh: tokenResp.RefreshToken,
			}
			if tokenResp.ExpiresIn > 0 {
				creds.Expires = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Unix()
			}
			return creds, nil

		case "slow_down":
			// Rate limited - increase interval
			// || 限流 - 增加轮询间隔
			interval += 5
			if opts.OnProgress != nil {
				opts.OnProgress("Rate limited, slowing down...")
			}
		case "authorization_pending":
			// User hasn't authorized yet - continue polling
			// || 用户尚未授权 - 继续轮询
			if opts.OnProgress != nil {
				opts.OnProgress("Waiting for authorization...")
			}
		case "expired_token":
			// Token expired
			// || 令牌过期
			return Credentials{}, fmt.Errorf("device code expired")
		case "access_denied":
			// User denied access
			// || 用户拒绝访问
			return Credentials{}, fmt.Errorf("access denied")
		default:
			// Unknown error
			// || 未知错误
			return Credentials{}, fmt.Errorf("device code error: %s - %s", tokenResp.Error, tokenResp.ErrorDesc)
		}
	}
}

// DeviceCodePollOptions configures device code flow polling.
// || 配置设备码流程的轮询选项
type DeviceCodePollOptions struct {
	TokenURL   string        // 令牌端点 URL
	ClientID   string        // 客户端 ID
	DeviceCode string        // 设备码
	Scopes     []string      // 权限范围
	Interval   int           // 轮询间隔（秒）
	ExpiresIn  int           // 过期时间（秒）
	Deadline   time.Duration // 最大等待时间
	OnProgress func(string)  // 进度回调函数
}
