// Example: 多租户隔离（Isolate）
//
// 模拟一个 SaaS 场景：多个用户共享同一个 Agent 服务，但每个用户有：
//   - 独立的 Session（互不干扰）
//   - 独立的 Memory（数据隔离）
//   - 独立的 Approval 配置（权限差异）
//   - 共享的 Model（成本集中管理）
//   - 共享的 Tools（能力统一）
//
// 运行：go run ./examples/multitenant_demo
package main

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/hycjack/crux-kernel/container"
	"github.com/hycjack/crux-kernel/events"
)

// --- 共享服务（注册在 root）---

// SharedModel 模拟共享的 LLM Model
type SharedModel struct {
	Provider string
	ModelID  string
}

// SharedTools 模拟共享的工具集
type SharedTools struct {
	Tools []string
}

// --- 租户独有服务（注册在 isolate）---

// UserSession 用户会话（每个用户独立）
type UserSession struct {
	UserID string
	Msgs  []string
}

func (s *UserSession) Add(msg string) {
	s.Msgs = append(s.Msgs, msg)
}

// UserMemory 用户记忆（每个用户独立）
type UserMemory struct {
	UserID string
	Data   map[string]string
}

// UserApproval 用户审批配置（每个用户独立）
type UserApproval struct {
	UserID    string
	StrictLvl string // "none" / "normal" / "strict"
}

// --- 模拟用户请求 ---

func handleUserRequest(ctx context.Context, c *Container, userID, input string) string {
	// 1. 从容器拿共享 Model（继承自 root）
	var model *SharedModel
	if err := c.Get(&model); err != nil {
		return fmt.Sprintf("[%s] error: %v", userID, err)
	}

	// 2. 从容器拿用户独有 Session
	var session *UserSession
	if err := c.Get(&session); err != nil {
		return fmt.Sprintf("[%s] no session: %v", userID, err)
	}
	session.Add(input)

	// 3. 从容器拿用户独有 Memory
	var memory *UserMemory
	_ = c.Get(&memory)

	// 4. 从容器拿用户独有 Approval
	var approval *UserApproval
	_ = c.Get(&approval)

	// 5. 从容器拿共享 Tools
	var tools *SharedTools
	_ = c.Get(&tools)

	// 6. Emit 一个事件到用户自己的 EventBus
	_, _ = c.Events().Emit(ctx, "user_request", events.SimpleEvent{
		EventType: "user_request",
		EventData: map[string]any{
			"user":   userID,
			"input":  input,
			"model":  model.ModelID,
			"strict": approval.StrictLvl,
		},
	})

	return fmt.Sprintf("[%s] model=%s tools=%d msgs=%d memory=%d strict=%s",
		userID, model.ModelID, len(tools.Tools), len(session.Msgs), len(memory.Data), approval.StrictLvl)
}

func main() {
	root := container.New()
	ctx := context.Background()

	// === 1. 注册共享服务到 root ===
	root.Register(&SharedModel{Provider: "openai", ModelID: "gpt-4o"})
	root.Register(&SharedTools{Tools: []string{"bash", "read_file", "write_file"}})

	// === 2. 为每个用户创建 Isolate 容器 ===
	users := []struct {
		id      string
		strict  string
		memory  map[string]string
	}{
		{"alice", "normal", map[string]string{"lang": "zh", "tz": "UTC+8"}},
		{"bob", "strict", map[string]string{"lang": "en", "tz": "UTC-5"}},
		{"carol", "none", map[string]string{"lang": "ja", "tz": "UTC+9"}},
	}

	userContainers := make(map[string]*container.Container, len(users))
	for _, u := range users {
		// 创建隔离容器
		c := root.Isolate(u.id)

		// 注册用户独有服务
		c.Register(&UserSession{UserID: u.id})
		c.Register(&UserMemory{UserID: u.id, Data: u.memory})
		c.Register(&UserApproval{UserID: u.id, StrictLvl: u.strict})

		// 订阅用户自己的事件（每个用户独立 EventBus）
		c.Events().On("user_request", events.DispatchSerial, func(ctx context.Context, evt events.Event) (any, error) {
			fmt.Fprintf(os.Stderr, "  [%s event] %s\n", u.id, evt.Type())
			return nil, nil
		})

		userContainers[u.id] = c
	}

	// === 3. 模拟并发请求 ===
	fmt.Fprintf(os.Stderr, "=== 多租户并发请求 ===\n")
	var wg sync.WaitGroup
	for _, u := range users {
		wg.Add(1)
		go func(userID string) {
			defer wg.Done()
			c := userContainers[userID]
			for i := 0; i < 3; i++ {
				input := fmt.Sprintf("msg-%d", i)
				result := handleUserRequest(ctx, c, userID, input)
				fmt.Fprintln(os.Stderr, result)
			}
		}(u.id)
	}
	wg.Wait()

	// === 4. 验证隔离性 ===
	fmt.Fprintf(os.Stderr, "\n=== 隔离性验证 ===\n")

	// alice 的 session 应有 3 条消息
	aliceSession, _ := getUserSession(userContainers["alice"])
	if len(aliceSession.Msgs) != 3 {
		fmt.Fprintf(os.Stderr, "alice session 应有 3 条消息，got %d\n", len(aliceSession.Msgs))
	}

	// bob 的 memory 应保留初始数据
	bobMemory, _ := getUserMemory(userContainers["bob"])
	if bobMemory.Data["lang"] != "en" {
		fmt.Fprintf(os.Stderr, "bob memory.lang 应为 en，got %s\n", bobMemory.Data["lang"])
	}

	// carol 的 approval 应为 none
	carolApproval, _ := getUserApproval(userContainers["carol"])
	if carolApproval.StrictLvl != "none" {
		fmt.Fprintf(os.Stderr, "carol approval 应为 none，got %s\n", carolApproval.StrictLvl)
	}

	// 父容器拿不到子容器的 UserSession（验证隔离）
	var parentSession *UserSession
	if err := root.Get(&parentSession); err == nil {
		fmt.Fprintf(os.Stderr, "root 不应拿到子容器的 UserSession\n")
	}

	fmt.Fprintf(os.Stderr, "所有用户隔离正常\n")

	// === 5. 级联清理 ===
	fmt.Fprintf(os.Stderr, "\n=== 级联清理 ===\n")
	if err := root.Dispose(); err != nil {
		fmt.Fprintf(os.Stderr, "Dispose 失败: %v\n", err)
	}
	// 所有子容器应进入 disposed
	for _, u := range users {
		c := userContainers[u.id]
		if c.State() != container.StateDisposed {
			fmt.Fprintf(os.Stderr, "%s 的容器应为 disposed，got %s\n", u.id, c.State())
		}
	}
	fmt.Fprintf(os.Stderr, "所有子容器已级联清理\n")
	fmt.Fprintf(os.Stderr, "\n=== 完成 ===\n")
}

// 用辅助函数避免在 main 中重复 Get 逻辑
type Container = container.Container

func getUserSession(c *container.Container) (*UserSession, error) {
	var s *UserSession
	err := c.Get(&s)
	return s, err
}

func getUserMemory(c *container.Container) (*UserMemory, error) {
	var m *UserMemory
	err := c.Get(&m)
	return m, err
}

func getUserApproval(c *container.Container) (*UserApproval, error) {
	var a *UserApproval
	err := c.Get(&a)
	return a, err
}
