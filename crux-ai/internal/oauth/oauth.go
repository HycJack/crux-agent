/*
 * 功能说明：OAuth 提供者注册和管理
 *
 * 解决的问题：
 * 1. 需要管理多个 OAuth 提供者的注册和查找
 * 2. 需要线程安全的提供者注册表
 * 3. 需要自动刷新过期令牌的机制
 * 4. 需要统一的 API Key 获取接口
 *
 * 解决方案：
 * 1. 使用 map 存储提供者，配合读写锁保证线程安全
 * 2. 提供 Register/Unregister/Get/List 等管理函数
 * 3. GetAPIKey 函数自动检测令牌过期并刷新
 * 4. 内置注册 Anthropic、GitHub Copilot、OpenAI Codex 提供者
 *
 * 应用场景：
 * - AI Agent 初始化时注册 OAuth 提供者
 * - 获取 API Key 时自动处理令牌刷新
 * - 多提供者的动态管理
 */
package oauth

import (
	"context"
	"fmt"
	"sync"
	"time"
)

var (
	providers = make(map[string]*ProviderInterface) // OAuth 提供者注册表
	mu        sync.RWMutex                          // 读写锁，保证线程安全
)

// Register registers an OAuth provider.
// || 注册一个 OAuth 提供者
// 参数：
//
//	provider - 提供者接口指针
func Register(provider *ProviderInterface) {
	mu.Lock()
	defer mu.Unlock()
	providers[provider.ID] = provider
}

// Unregister removes an OAuth provider.
// || 注销一个 OAuth 提供者
// 参数：
//
//	id - 提供者 ID
func Unregister(id string) {
	mu.Lock()
	defer mu.Unlock()
	delete(providers, id)
}

// Reset removes all OAuth providers.
// || 重置注册表，移除所有 OAuth 提供者
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	providers = make(map[string]*ProviderInterface)
}

// Get returns an OAuth provider by ID.
// || 根据 ID 获取 OAuth 提供者
// 参数：
//
//	id - 提供者 ID
//
// 返回：
//
//	提供者接口指针和错误信息
func Get(id string) (*ProviderInterface, error) {
	mu.RLock()
	defer mu.RUnlock()
	p, ok := providers[id]
	if !ok {
		return nil, fmt.Errorf("oauth provider not found: %s", id)
	}
	return p, nil
}

// List returns all registered OAuth provider IDs.
// || 返回所有已注册的 OAuth 提供者 ID
// 返回：
//
//	提供者 ID 列表
func List() []string {
	mu.RLock()
	defer mu.RUnlock()
	ids := make([]string, 0, len(providers))
	for id := range providers {
		ids = append(ids, id)
	}
	return ids
}

// GetAPIKey returns a valid API key, refreshing the token if expired.
// || 获取有效的 API Key，如果令牌过期则自动刷新
// 参数：
//
//	ctx - 上下文
//	providerID - 提供者 ID
//	credentials - 凭证信息
//
// 返回：
//
//	API Key 和错误信息
func GetAPIKey(ctx context.Context, providerID string, credentials Credentials) (string, error) {
	p, err := Get(providerID)
	if err != nil {
		return "", err
	}

	// Check if token is expired (with 5 minute buffer)
	// || 检查令牌是否过期（预留5分钟缓冲）
	if credentials.Expires > 0 && time.Now().Unix() > credentials.Expires-300 {
		if p.RefreshToken != nil {
			credentials, err = p.RefreshToken(ctx, credentials)
			if err != nil {
				return "", fmt.Errorf("failed to refresh token: %w", err)
			}
		}
	}

	// Use provider's GetAPIKey if available, otherwise return access token directly
	// || 如果提供者有自定义的 GetAPIKey 函数则使用，否则直接返回访问令牌
	if p.GetAPIKey != nil {
		return p.GetAPIKey(ctx, credentials)
	}

	return credentials.Access, nil
}

// init registers built-in OAuth providers when the package is loaded.
// || 包初始化时注册内置的 OAuth 提供者
func init() {
	// Register Anthropic OAuth provider
	// || 注册 Anthropic OAuth 提供者
	Register(&ProviderInterface{
		ID:   "anthropic",
		Name: "Anthropic",
		Login: func(ctx context.Context, callbacks LoginCallbacks) (Credentials, error) {
			return loginAnthropic(ctx, callbacks)
		},
		RefreshToken: func(ctx context.Context, credentials Credentials) (Credentials, error) {
			return refreshAnthropic(ctx, credentials)
		},
		GetAPIKey: func(ctx context.Context, credentials Credentials) (string, error) {
			return credentials.Access, nil
		},
	})

	// Register GitHub Copilot OAuth provider
	// || 注册 GitHub Copilot OAuth 提供者
	Register(&ProviderInterface{
		ID:   "github-copilot",
		Name: "GitHub Copilot",
		Login: func(ctx context.Context, callbacks LoginCallbacks) (Credentials, error) {
			return loginGitHubCopilot(ctx, callbacks)
		},
		RefreshToken: func(ctx context.Context, credentials Credentials) (Credentials, error) {
			return refreshGitHubCopilot(ctx, credentials)
		},
		GetAPIKey: func(ctx context.Context, credentials Credentials) (string, error) {
			return credentials.Access, nil
		},
	})

	// Register OpenAI Codex OAuth provider
	// || 注册 OpenAI Codex OAuth 提供者
	Register(&ProviderInterface{
		ID:   "openai-codex",
		Name: "OpenAI Codex",
		Login: func(ctx context.Context, callbacks LoginCallbacks) (Credentials, error) {
			return loginOpenAICodex(ctx, callbacks)
		},
		RefreshToken: func(ctx context.Context, credentials Credentials) (Credentials, error) {
			return refreshOpenAICodex(ctx, credentials)
		},
		GetAPIKey: func(ctx context.Context, credentials Credentials) (string, error) {
			return credentials.Access, nil
		},
	})
}
