// Package router provides role-based model selection for crux-ai.
//
// The RoleRouter sits on top of the Provider Registry and answers
// the question "for this Role, which Provider+Model should I use?".
// A typical agent loop has 3-5 roles wired in YAML config and
// the loop calls router.Stream(ctx, RoleDefault, ...) or
// router.Stream(ctx, RolePlan, ...) depending on context.
//
// Why roles instead of hard-coded model names? Because roles
// describe intent ("a small fast model") while models are
// implementation ("gpt-4o-mini-2024-07-18"). Roles let the same
// agent code run against different model catalogs without edits,
// and let operators A/B test by swapping Config without restarting
// agent code.
package router

import (
	"context"
	"fmt"
	"sync"

	"github.com/hycjack/crux-ai/core"
)

// Role describes the kind of model a caller wants.
// || 描述调用者想要的模型类型
type Role string

const (
	// RoleDefault is the unspecified / general-purpose role.
	// || 默认/通用角色
	RoleDefault Role = "default"

	// RoleSmol picks a small, fast, cheap model.
	// || 小型快速模型，适合高吞吐量任务
	RoleSmol Role = "smol"

	// RoleSlow picks a large, expensive, high-quality model.
	// || 大型高质量模型，适合复杂任务
	RoleSlow Role = "slow"

	// RolePlan picks a model with extended reasoning.
	// || 推理模型，适合规划任务
	RolePlan Role = "plan"

	// RoleCommit picks a model specialized for code edits.
	// || 代码编辑专用模型
	RoleCommit Role = "commit"
)

// AllRoles is the canonical iteration order for diagnostics.
var AllRoles = []Role{RoleDefault, RoleSmol, RoleSlow, RolePlan, RoleCommit}

// IsValidRole reports whether r is a known role string.
func IsValidRole(r Role) bool {
	for _, k := range AllRoles {
		if k == r {
			return true
		}
	}
	return false
}

// ModelSpec is a minimal model reference for role mapping.
// || 角色映射的最小模型引用
type ModelSpec struct {
	// ID is the model identifier (e.g. "gpt-4o").
	ID string
	// Provider is the KnownProvider (e.g. "openai").
	Provider core.KnownProvider
	// API is the KnownAPI (e.g. "openai-completions").
	API core.KnownAPI
}

// IsZero reports whether the ModelSpec is the zero value.
func (m ModelSpec) IsZero() bool {
	return m.ID == ""
}

// Config is the role-to-model mapping.
// || 角色到模型的映射配置
type Config struct {
	Default ModelSpec
	Smol    ModelSpec
	Slow    ModelSpec
	Plan    ModelSpec
	Commit  ModelSpec
}

// Materialize returns a non-nil ModelSpec for every role,
// substituting Default where the role's slot is empty.
func (c Config) Materialize() (map[Role]ModelSpec, error) {
	out := make(map[Role]ModelSpec, 5)
	out[RoleDefault] = c.Default
	if !c.Smol.IsZero() {
		out[RoleSmol] = c.Smol
	} else {
		out[RoleSmol] = c.Default
	}
	if !c.Slow.IsZero() {
		out[RoleSlow] = c.Slow
	} else {
		out[RoleSlow] = c.Default
	}
	if !c.Plan.IsZero() {
		out[RolePlan] = c.Plan
	} else {
		out[RolePlan] = c.Default
	}
	if !c.Commit.IsZero() {
		out[RoleCommit] = c.Commit
	} else {
		out[RoleCommit] = c.Default
	}
	if out[RoleDefault].IsZero() {
		return nil, fmt.Errorf("router.Config: Config.Default is empty")
	}
	return out, nil
}

// Resolved is the result of resolving a role to a concrete model.
// || 角色解析结果
type Resolved struct {
	Spec  ModelSpec
	Model core.Model
}

// RoleRouter picks a ModelSpec for a given Role by consulting its Config.
// || 角色路由器，根据配置选择模型
type RoleRouter struct {
	mu  sync.RWMutex
	cfg Config
}

// New returns a RoleRouter with the given config.
func New(cfg Config) (*RoleRouter, error) {
	if _, err := cfg.Materialize(); err != nil {
		return nil, err
	}
	return &RoleRouter{cfg: cfg}, nil
}

// MustNew is like New but panics on error.
func MustNew(cfg Config) *RoleRouter {
	r, err := New(cfg)
	if err != nil {
		panic(err)
	}
	return r
}

// SetConfig atomically replaces the role mapping.
func (r *RoleRouter) SetConfig(cfg Config) error {
	if _, err := cfg.Materialize(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cfg = cfg
	return nil
}

// Config returns a snapshot of the current mapping.
func (r *RoleRouter) Config() Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfg
}

// Resolve returns the ModelSpec for the given role.
func (r *RoleRouter) Resolve(role Role) (Resolved, error) {
	if !IsValidRole(role) {
		return Resolved{}, fmt.Errorf("router.RoleRouter.Resolve: unknown role %q", role)
	}
	r.mu.RLock()
	cfg := r.cfg
	r.mu.RUnlock()

	models, err := cfg.Materialize()
	if err != nil {
		return Resolved{}, err
	}
	spec, ok := models[role]
	if !ok || spec.IsZero() {
		return Resolved{}, fmt.Errorf("router.RoleRouter.Resolve: no model for role %q", role)
	}
	return Resolved{
		Spec: spec,
		Model: core.Model{
			ID:       spec.ID,
			Provider: spec.Provider,
			API:      spec.API,
		},
	}, nil
}

// Stream is a convenience wrapper that resolves the role and calls Stream.
// || 流式调用，自动根据角色选择模型
func (r *RoleRouter) Stream(ctx context.Context, role Role, msgs []core.Message, opts core.StreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
	resolved, err := r.Resolve(role)
	if err != nil {
		return nil, err
	}
	provider, err := core.GetProvider(resolved.Spec.API)
	if err != nil {
		return nil, err
	}
	llmCtx := core.Context{Messages: msgs}
	return provider.Stream(ctx, resolved.Model, llmCtx, opts)
}

// Complete is the non-streaming equivalent of Stream.
func (r *RoleRouter) Complete(ctx context.Context, role Role, msgs []core.Message, opts core.StreamOptions) (core.AssistantMessage, error) {
	resolved, err := r.Resolve(role)
	if err != nil {
		return core.AssistantMessage{}, err
	}
	provider, err := core.GetProvider(resolved.Spec.API)
	if err != nil {
		return core.AssistantMessage{}, err
	}
	llmCtx := core.Context{Messages: msgs}
	stream, err := provider.Stream(ctx, resolved.Model, llmCtx, opts)
	if err != nil {
		return core.AssistantMessage{}, err
	}
	return stream.Result()
}
