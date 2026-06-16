/*
 * 功能说明：OAuth 认证工具包类型定义
 *
 * 解决的问题：
 * 1. 需要统一的 OAuth 凭证数据结构来存储访问令牌和刷新令牌
 * 2. 需要定义 OAuth 提供者接口，支持多种认证流程
 * 3. 需要回调机制来处理登录过程中的用户交互
 *
 * 解决方案：
 * 1. 定义 Credentials 结构体存储令牌信息
 * 2. 定义 ProviderInterface 接口统一不同提供者的认证流程
 * 3. 定义 LoginCallbacks 结构体提供登录过程中的回调函数
 *
 * 应用场景：
 * - AI 提供者的 OAuth 认证（Anthropic、GitHub Copilot、OpenAI Codex）
 * - 设备码认证流程
 * - 令牌刷新和管理
 */
// Package oauth provides OAuth authentication flows for AI providers.
// || 为 AI 提供者提供 OAuth 认证流程
package oauth

import "context"

// Credentials holds OAuth tokens.
// || 存储 OAuth 令牌信息
type Credentials struct {
	Refresh  string            `json:"refresh"`  // 刷新令牌
	Access   string            `json:"access"`   // 访问令牌
	Expires  int64             `json:"expires"`  // 过期时间（Unix 时间戳）
	Extra    map[string]any    `json:"extra"`    // 额外数据（可选）
}

// ProviderInterface defines the interface for an OAuth provider.
// || 定义 OAuth 提供者的接口
type ProviderInterface struct {
	ID           string                                                                 // 提供者唯一标识
	Name         string                                                                 // 提供者显示名称
	Login        func(ctx context.Context, callbacks LoginCallbacks) (Credentials, error) // 登录函数
	RefreshToken func(ctx context.Context, credentials Credentials) (Credentials, error)  // 刷新令牌函数
	GetAPIKey    func(ctx context.Context, credentials Credentials) (string, error)      // 获取 API Key 函数
}

// LoginCallbacks provides callbacks for the OAuth login flow.
// || 提供 OAuth 登录流程的回调函数
type LoginCallbacks struct {
	OnAuth      func(url string)                            // 认证 URL 回调（用于引导用户访问）
	OnDeviceCode func(deviceCode string, verificationURI string) // 设备码回调（用于显示设备码和验证URL）
	OnPrompt    func(message string)                        // 提示信息回调
	OnProgress  func(message string)                        // 进度信息回调
	Signal      <-chan struct{}                             // 取消信号通道
}
