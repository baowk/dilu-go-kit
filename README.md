# dilu-go-kit

Go 微服务基础工具包。提供统一的服务启动、日志、中间件、错误码、服务发现、远程配置和事件通知。

## 特性

- **boot** — 一行启动服务（Config + DB + Redis + gRPC + 注册 + 远程配置 + 优雅关闭）
- **log** — 统一日志接口（slog 实现，traceId 自动注入，支持 console/file/both 输出）
- **mid** — 可配置中间件（Trace + Recovery + Logger + ErrorHandler + JWT + CORS + RateLimit）
- **resp** — 统一 HTTP 响应（Ok / Fail / Page / Error）+ 标准错误码
- **store** — 数据访问层基础类型（ListOpts 分页）
- **stream** — Redis Stream 基础封装（发布、消费组、读取、ACK、死信）
- **clientx** — 服务客户端基础能力（重试、熔断、gRPC 错误码映射）
- **migratex** — PostgreSQL SQL 迁移封装（up/down/version/force/create）
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
stream/     Redis Stream 基础封装
clientx/    服务客户端重试、熔断、错误码映射
migratex/   PostgreSQL SQL 迁移封装
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
r.Use(mid.Trace())         // traceId 生成/传递，X-Request-Id 仅作兼容别名
r.Use(mid.Recovery())      // panic 恢复
r.Use(mid.ErrorHandler())  // AppError 捕获
r.Use(mid.Logger())        // 请求日志（method/path/status/latency/traceId）
r.Use(mid.CORS())          // CORS（支持 whitelist）
r.Use(mid.RateLimit(100, time.Minute))  // 限流

// 可配置限流：默认 memory；传 Redis client 后可切换分布式限流
r.Use(mid.RateLimitFromConfig(mid.AccessLimitCfg{
    Enable: true,
    Total: 300,
    Duration: 5,
    Backend: "redis",
    Redis: app.Redis,
    KeyPrefix: "gateway:ratelimit",
}))

// JWT 认证
auth := r.Group("/v1").Use(mid.JWT(mid.JWTConfig{Secret: jwtSecret}))
uid := mid.GetUID(c)
tenantID := mid.GetTenantID(c)
shopIDs := mid.GetShopIDs(c)
scopes := mid.GetScopes(c)
nickname := mid.GetNickname(c)

// 仅在可信网关已剥离外部身份头时开启 HeaderUID 信任模式
r.Group("/internal").Use(mid.JWT(mid.JWTConfig{
    HeaderUID:      "x-user-id",
    HeaderTenantID: "x-tenant-id",
    HeaderShopIDs:  "x-shop-ids", // "1,2,3"
    HeaderScopes:   "x-scopes",   // "order.read,order.write"
    TrustHeaderUID: true,
}))

// gRPC traceId 透传
conn, _ := grpc.NewClient(addr, grpc.WithUnaryInterceptor(mid.GRPCUnaryClientInterceptor()))
```

gRPC 客户端默认明文，仅适合可信内网；跨网络或零信任环境请传 TLS：

```go
conn, _ := grpcx.Dial(addr, grpcx.DialOption{
    TLSConfig: &tls.Config{ServerName: "mf-user.internal"},
    RetryMaxAttempts: 3,
})
```

## 服务客户端

```go
import "github.com/baowk/dilu-go-kit/clientx"

breaker := clientx.NewBreaker(clientx.BreakerConfig{
    FailureThreshold: 5,
    Cooldown: 30 * time.Second,
})

err := breaker.Do(ctx, func(ctx context.Context) error {
    return clientx.Do(ctx, clientx.RetryConfig{MaxAttempts: 3}, func(ctx context.Context) error {
        // call downstream service
        return nil
    })
})
if err != nil {
    err = clientx.MapGRPCError(err)
    code := clientx.CodeOf(err)
    _ = code
}
```

## 标准错误码

```go
resp.Fail(c, resp.CodeUnauthorized, "未登录")   // 40101
resp.Fail(c, resp.CodeForbidden, "无权操作")     // 40301
resp.Fail(c, resp.CodeInvalidParam, "参数错误")  // 40001
resp.Fail(c, resp.CodeNotFound, "不存在")        // 40401
resp.Fail(c, resp.CodeInternal, "服务错误")      // 50000
resp.FailStatus(c, http.StatusUnauthorized, resp.CodeUnauthorized, "未登录")
```

## 事件通知

```go
import "github.com/baowk/dilu-go-kit/notify"

notify.Init("http://mf-ws:9020")
notify.Send("env", map[string]any{"action": "created", "env_id": 123, "workspace_id": 1})
notify.SendContext(ctx, "proxy", payload)  // 自动携带 traceId
```

## Redis Stream

```go
import "github.com/baowk/dilu-go-kit/stream"

_ = stream.EnsureGroup(ctx, app.Redis, "sync.tasks", "sync-worker", "0")

id, err := stream.Publish(ctx, app.Redis, "sync.tasks", map[string]any{
    "task_id": "123",
    "type": "order_pull",
})

msgs, err := stream.ReadGroup(ctx, app.Redis, stream.ReaderConfig{
    Stream: "sync.tasks",
    Group: "sync-worker",
    Consumer: "worker-1",
})
for _, msg := range msgs {
    // handle message idempotently
    _ = stream.Ack(ctx, app.Redis, msg.Stream, "sync-worker", msg.ID)
}
```

## 数据库迁移

推荐每个服务独立维护 `migrations/`：

```text
services/order-service/
  migrations/
    20260709120000_init.up.sql
    20260709120000_init.down.sql
```

```bash
go run github.com/baowk/dilu-go-kit/cmd/migrate -dir services/order-service/migrations create -name init
DATABASE_DSN='postgres://user:pass@127.0.0.1:5432/order_db?sslmode=disable' \
  go run github.com/baowk/dilu-go-kit/cmd/migrate -dir services/order-service/migrations up
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

同一服务多实例可用节点级配置区分：

```bash
REMOTE_NODE=node-1 SERVER_ADDR=:7801 SERVER_ADVERTISE_ADDR=10.0.1.5:7801 ./my-service
REMOTE_NODE=node-2 SERVER_ADDR=:7802 SERVER_ADVERTISE_ADDR=10.0.1.6:7802 ./my-service
```

对应 KV：

```text
/config/mf-user        # 服务级共享配置
/config/mf-user/node-1 # node-1 覆盖
/config/mf-user/node-2 # node-2 覆盖
```

## 敏感配置

密码、token、JWT secret 不建议写入 YAML 或远程配置。可用环境变量覆盖，优先级高于本地和远程配置：

```bash
DATABASE_DSN='host=... user=... password=... dbname=...'
DATABASE_MAIN_DSN='host=... user=... password=... dbname=...'
REDIS_PASSWORD='...'
JWT_SECRET='...'
REGISTRY_TOKEN='...'
NOTIFY_WS_URL='http://mf-ws:9020'
REMOTE_NODE='node-1'
```

## 配置示例

```yaml
server:
  name: my-service
  addr: ":8080"                  # 监听地址
  # advertiseAddr: "10.0.1.5:8080" # 注册发现地址；空时从 addr 推断
  mode: debug

database:
  main:
    dsn: ""                    # 建议用 DATABASE_MAIN_DSN / DATABASE_DSN 注入
    slowThreshold: 200

redis:
  addr: "127.0.0.1:6379"
  username: ""                # Redis 6+ ACL（可选）
  password: ""                # 建议用 REDIS_PASSWORD 注入

log:
  output: console             # console / file / both
  # file:
  #   path: "logs/app.log"

jwt:
  secret: ""                  # 建议用 JWT_SECRET 注入
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
  # token: ""                  # 建议用 REGISTRY_TOKEN 注入
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

模块拆分特别约束：一张表一个 `model/{table}.go` 和 `store/{table}_pg.go`；一类资源一个 `service/{resource}.go`、`service/dto/{resource}.go`、`apis/{resource}_api.go`。`store/store.go` 只放聚合和初始化，避免 AI 把多个资源写进单个大文件。

## License

MIT
