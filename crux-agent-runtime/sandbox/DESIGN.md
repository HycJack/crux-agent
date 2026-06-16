# 模块设计：Sandbox 沙箱系统

> 模块: crux-agent-runtime/sandbox
> 版本: v0.1.0 | 更新: 2026-06-17
> 状态: ⏳ 待实现

---

## 1. 职责

控制 Agent 可以访问什么：文件、命令、网络。默认拒绝。

## 2. 核心接口

```go
type Sandbox interface {
    CheckRead(path string) error
    CheckWrite(path string) error
    CheckExec(cmd string) error
    CheckNetwork(addr string) error
    Scope() Scope
}
```

## 3. 实现对比

| 实现 | 隔离级别 | 性能 | 使用场景 |
|------|----------|------|----------|
| NoneSandbox | 无 | 最快 | 开发/测试 |
| ProcessSandbox | 进程级 | 快 | 生产环境 |
| DockerSandbox | 容器级 | 中等 | 不可信代码 |
| FirecrackerSandbox | VM 级 | 慢 | 高安全需求 |

## 4. 第三方集成

| 服务 | 接入方式 | 说明 |
|------|----------|------|
| E2B | HTTP API | 云端沙箱 |
| Modal | SDK | 容器化沙箱 |
| Fly.io | API | 轻量级 VM |
| 自定义 | HTTP | 微服务模式 |

## 5. 权限检查流程

```
skill.Handle(ctx, call)
  │
  ├─ ctx.Sandbox.CheckExec("ls -la")
  │    │
  │    ├─ 检查 blockedCmds → 拒绝？
  │    ├─ 检查 allowedCmds → 允许？
  │    └─ 默认拒绝
  │
  └─ 执行命令（如果允许）
```

## 6. 配置示例

```yaml
sandbox:
  type: process
  read_only: ["/home/user/projects"]
  read_write: ["/home/user/projects/output"]
  allow_cmds: ["ls", "cat", "grep", "python3"]
  block_cmds: ["rm -rf", "sudo"]
  timeout: 300
```
