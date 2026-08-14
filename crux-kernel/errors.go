// Package runtime 提供 crux-kernel 的公共错误类型。
package runtime

import "errors"

// Sentinel errors
var (
	// ErrServiceNotFound 未注册该类型的服务时返回。
	ErrServiceNotFound = errors.New("crux-kernel: service not found")

	// ErrServiceExists 同类型服务已注册且未提供覆盖选项时返回。
	ErrServiceExists = errors.New("crux-kernel: service already exists")

	// ErrPluginNotFound 未注册该名称的插件时返回。
	ErrPluginNotFound = errors.New("crux-kernel: plugin not found")

	// ErrContainerDisposed 容器已卸载，不可再操作时返回。
	ErrContainerDisposed = errors.New("crux-kernel: container already disposed")

	// ErrPluginNotActive 插件不在 active 状态，无法 reload 时返回。
	ErrPluginNotActive = errors.New("crux-kernel: plugin not in active state")

	// ErrInvalidHandler handler 为 nil 时返回。
	ErrInvalidHandler = errors.New("crux-kernel: handler is nil")
)
