/*
 * 功能说明：PKCE（Proof Key for Code Exchange）工具
 * 
 * 解决的问题：
 * 1. OAuth 授权码流程中需要防止授权码被截获
 * 2. 需要安全的方式验证授权码的来源
 * 3. 需要生成符合规范的 verifier 和 challenge 对
 * 
 * 解决方案：
 * 1. 使用 32 字节随机数生成 verifier
 * 2. 使用 SHA-256 哈希生成 challenge
 * 3. 使用 base64url 编码（不带填充）
 * 
 * 应用场景：
 * - OAuth 授权码流程的安全性增强
 * - 移动应用和单页应用的认证
 * - 防止授权码被截获攻击
 */
package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// PKCE holds a PKCE verifier and challenge pair.
// || 存储 PKCE 的 verifier 和 challenge 对
type PKCE struct {
	Verifier  string `json:"verifier"`  // 验证器（用于后续验证）
	Challenge string `json:"challenge"` // 挑战值（发送给服务器）
}

// GeneratePKCE generates a PKCE code verifier and challenge.
// || 生成 PKCE 的 verifier 和 challenge 对
// 返回：
//   PKCE 结构体和错误信息
func GeneratePKCE() (PKCE, error) {
	// Generate random verifier (43-128 characters)
	// || 生成随机 verifier（43-128 字符）
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return PKCE{}, err
	}
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)

	// Generate challenge (S256)
	// || 生成 challenge（使用 SHA-256）
	h := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])

	return PKCE{
		Verifier:  verifier,
		Challenge: challenge,
	}, nil
}
