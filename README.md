# dilu-go-kit

Go 微服务基础工具包。提供统一的服务启动、日志、中间件、错误码、服务发现、远程配置和事件通知。

## 特性

- **boot** — 一行启动服务（Config + DB + Redis + gRPC + 注册 + 远程配置 + 优雅关闭）
- **log** — 统一日志接口（slog 实现，traceId 自动注入，支持 console/file/both 输出）
- **mid** — 可配置中间件（Trace + Recovery + Logger + ErrorHandler + JWT + CORS + RateLimit）
- **resp** — 统一 HTTP 响应（Ok / Fail / Page / Error）+ 标准错误码
- **store** — 数据访问层基础类型（ListOpts 分页）
- **registry** — 服务注册与发现（etcd / consul）
- **notify** — 通用 HTTP 事件推送（支持 traceId 透传）

## 安装

```bash
go get github.com/baowk/dilu-go-kit@latest
```

## 快速开始

```go
package main

import (
    "github.com/baowk/dilu-go-kit/boot"
    "github.com/baowk/dilu-go-kit/log"
    "github.com/baowk/dilu-go-kit/mid"
    "github.com/baowk/dilu-go-kit/resp"
    "github.com/gin-gonic/gin"
)

func main() {
    app, _ := boot.New("config.yaml")
    app.Run(func(a *boot.App) error {
        // 一行注册全部中间件（Trace + Recovery + ErrorHandler + Logger + CORS + RateLimit）
        mid.Default(a.Gin, mid.DefaultConfig{
            CORS:        mid.CORSCfg{Enable: true, Mode: "allow-all"},
            AccessLimit: mid.AccessLimitCfg{Enable: true, Total: 300, Duration: 5},
        })

        a.Gin.GET("/ping", func(c *gin.Context) {
            log.InfoContext(c.Request.Context(), "ping", "ip", c.ClientIP())
            resp.Ok(c, "pong")
        })
        return nil
    })
}
```

详见 [docs/quickstart.md](docs/quickstart.md) 和 [example/](example/)。

## 目录

```
boot/       服务启动（Config/DB/Redis/gRPC/Registry/RemoteConfig/Logger）
log/        统一日志接口（Logger 接口 + slog 实现 + traceId + lumberjack file rotation）
mid/        中间件（Trace/Recovery/Logger/ErrorHandler/JWT/CORS/RateLimit/Default/gRPC interceptor）
resp/       统一 HTTP 响应 + 标准错误码
store/      数据访问基础类型（ListOpts）
registry/   服务注册与发现（etcd / consul）
notify/     通用事件推送
example/    完整示例服务
docs/       规范文档
```

## 日志

```go
import "github.com/baowk/dilu-go-kit/log"

log.Info("server started", "port", 8080)
log.InfoContext(ctx, "created env", "env_id", 123)  // 自动带 trace_id
log.With("module", "auth").Warn("token expired")
```

支持三种输出模式，通过配置切换：

```yaml
log:
  output: console   # console（默认）| file | both
  file:
    path: "logs/app.log"
    maxSize: 100    # MB/文件（默认 100）
    maxAge: 7       # 保留天数（默认 7）
    maxBackups: 5   # 旧文件数（默认 5）
    compress: false # gzip 压缩
```

底层使用 slog（Go 标准库），文件输出使用 lumberjack 自动 rotation。通过 `log.SetLogger()` 可替换为任意实现。

## 中间件

```go
// 方式一：一行全部注册（推荐）
mid.Default(r, mid.DefaultConfig{...})

// 方式二：单独使用
r.Use(mid.Trace())         // traceId 生成/传递
r.Use(mid.Recovery())      // panic 恢复
r.Use(mid.ErrorHandler())  // AppError 捕获
r.Use(mid.Logger())        // 请求日志（method/path/status/latency/traceId）
r.Use(mid.CORS())          // CORS（支持 whitelist）
r.Use(mid.RateLimit(100, time.Minute))  // 限流

// JWT 认证
auth := r.Group("/v1").Use(mid.JWT(mid.JWTConfig{Secret: "xxx", HeaderUID: "a_uid"}))
uid := mid.GetUID(c)
nickname := mid.GetNickname(c)

// gRPC traceId 透传
conn, _ := grpc.NewClient(addr, grpc.WithUnaryInterceptor(mid.GRPCUnaryClientInterceptor()))
```

## 标准错误码

```go
resp.Fail(c, resp.CodeUnauthorized, "未登录")   // 40101
resp.Fail(c, resp.CodeForbidden, "无权操作")     // 40301
resp.Fail(c, resp.CodeInvalidParam, "参数错误")  // 40001
resp.Fail(c, resp.CodeNotFound, "不存在")        // 40401
resp.Fail(c, resp.CodeInternal, "服务错误")      // 50000
```

## 事件通知

```go
import "github.com/baowk/dilu-go-kit/notify"

notify.Init("http://mf-ws:9020")
notify.Send("env", map[string]any{"action": "created", "env_id": 123, "workspace_id": 1})
notify.SendContext(ctx, "proxy", payload)  // 自动携带 traceId
```

## 服务注册与发现

支持 etcd 和 consul 两种后端：

```yaml
# etcd
registry:
  enable: true
  type: etcd
  endpoints: ["127.0.0.1:2379"]

# consul
registry:
  enable: true
  type: consul
  address: "127.0.0.1:8500"
  token: ""                    # ACL token（可选）
```

服务启动自动注册，关闭自动注销。网关 Watch 实时发现变更。

## 远程配置

复用 registry 连接，从 etcd/consul KV 加载配置并热更新：

```yaml
registry:
  enable: true
  type: etcd
  endpoints: ["127.0.0.1:2379"]
  configKey: "/config/"        # 有值即启用，自动拼接 server.name
  configNode: ""               # 节点级覆盖（可选，或 env REMOTE_NODE）
```

三层深度合并（每层只覆盖它有的 key）：
```
本地 YAML → /config/mf-user → /config/mf-user/node-1
```

运行时自动 watch，KV 变更秒级生效：

```go
app.OnConfigChange(func(cfg *boot.Config) error {
    log.Info("config updated", "redis", cfg.Redis.Addr)
    return nil  // 返回 error 可拒绝此次更新
})
```

## 配置示例

```yaml
server:
  name: my-service
  addr: ":8080"
  mode: debug

database:
  main:
    dsn: "host=127.0.0.1 user=postgres dbname=mydb sslmode=disable"
    slowThreshold: 200

redis:
  addr: "127.0.0.1:6379"
  username: ""                # Redis 6+ ACL（可选）
  password: ""

log:
  output: console             # console / file / both
  # file:
  #   path: "logs/app.log"

jwt:
  secret: "your-secret"
  expires: 1440

cors:
  enable: true
  mode: allow-all

accessLimit:
  enable: true
  total: 300
  duration: 5

notify:
  wsUrl: "http://mf-ws:9020"

registry:
  enable: true
  type: etcd                  # etcd / consul
  endpoints: ["127.0.0.1:2379"]
  # address: "127.0.0.1:8500"  # consul
  configKey: "/config/"       # 启用远程配置
  # configNode: "node-1"      # 节点级覆盖
```

## AI 辅助开发

本仓库提供 [CLAUDE.template.md](CLAUDE.template.md) 作为 AI 开发规范模板：

| AI 工具 | 文件名 |
|---------|--------|
| Claude Code | `CLAUDE.md` |
| OpenAI Codex CLI | `AGENTS.md` |
| Cursor | `.cursorrules` |
| Windsurf | `.windsurfrules` |
| GitHub Copilot | `.github/copilot-instructions.md` |

```bash
curl -sL https://raw.githubusercontent.com/baowk/dilu-go-kit/main/CLAUDE.template.md > CLAUDE.md
```

## 规范

- [开发规范](docs/conventions.md) — 项目结构、数据层、API、错误码、中间件
- [快速开始](docs/quickstart.md) — 5 分钟上手

## License

MIT
