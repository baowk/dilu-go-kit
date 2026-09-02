# dilu-go-kit

Go 微服务基础工具包。提供统一的服务启动、日志、中间件、错误码、服务发现、远程配置和事件通知。

## 特性

- **boot** — 一行启动服务（Config + DB + Redis + gRPC + 注册 + 远程配置 + 优雅关闭）
- **boot.Component** — 统一后台任务启动、停止和反向关闭
- **log** — 统一日志接口（slog 实现，traceId 自动注入，支持 console/file/both 输出）
- **mid** — 可配置中间件（Trace + Recovery + Logger + ErrorHandler + JWT + CORS + RateLimit）
- **resp** — 统一 HTTP 响应（Ok / Fail / Page / Error）+ 标准错误码
- **apperr** — transport-neutral 业务错误及 HTTP/gRPC 安全映射
- **store** — 数据访问层基础类型（ListOpts 分页）
- **stream** — Redis Stream 基础封装（发布、消费组、读取、ACK、死信）
- **stream tracing** — 发布时注入 W3C trace context，消费时可恢复上游上下文
- **clientx** — 服务客户端基础能力（重试、熔断、gRPC 错误码映射）
- **migratex** — PostgreSQL SQL 迁移封装（up/down/version/force/create）
- **registry** — 服务注册与发现（etcd / consul）
- **registry.Resolver** — 客户端 Watch 缓存、RoundRobin/Random 负载均衡与可选版本/标签过滤
- **notify** — 通用 HTTP 事件推送（支持 traceId 透传）
- **metrics** — Prometheus HTTP/gRPC 指标（支持运行时服务名）
- **dependency metrics** — 统一出站依赖请求计数和延迟指标
- **clientx.HTTPClient** — HTTP 出站超时、幂等重试、熔断与 trace 透传
- **telemetry** — 可选 OpenTelemetry tracing（OTLP HTTP + W3C 传播，默认 no-op）
- **diagnostics** — 显式注册 pprof 运行时诊断接口（默认关闭）
- **Proto/Buf** — 契约优先生成 Go、gRPC、HTTP Gateway 和 OpenAPI
- **cmd/dilu** — AI 友好的服务与资源脚手架
- **grpcx.DialService** — 注册中心服务名拨号 + 健康实例 round_robin

## 安装

要求 Go 1.27.0 或更高版本。

```bash
go get github.com/baowk/dilu-go-kit@latest
```

## AI 友好的契约和脚手架

服务 API 建议使用 `api/<service>/v1/*.proto` 作为唯一契约，通过 Buf 生成 Go、gRPC、HTTP
Gateway 和 OpenAPI：

```bash
make buf-install       # 将固定版本 Buf 安装到 ./bin
make proto-lint
make proto-generate    # 输出到 gen/（已加入 .gitignore）
```

新服务和资源可由脚手架生成，生成结果遵循本仓库的分层约束：

```bash
go run ./cmd/dilu new service inventory --dir ./services/inventory --module example.com/inventory
go run ./cmd/dilu add resource task --dir ./services/inventory --module inventory
go run ./cmd/dilu generate --dir ./services/inventory
```

脚手架会拒绝覆盖已有文件；业务代码只应补充 `internal/<module>/biz`、`data`、`service` 和
`server`，不要手改生成的 Proto transport 文件。CI 可执行 `make proto-lint proto-generate`
并检查生成结果无漂移。

## 快速开始

```go
package main

import (
    "log"
    "net/http"

    "github.com/baowk/dilu-go-kit/boot"
    kitlog "github.com/baowk/dilu-go-kit/log"
    "github.com/baowk/dilu-go-kit/metrics"
    "github.com/baowk/dilu-go-kit/mid"
    "github.com/baowk/dilu-go-kit/resp"
    "github.com/gin-gonic/gin"
)

func main() {
    app, err := boot.New("config.yaml")
    if err != nil {
        log.Fatal(err)
    }
    if err := app.Run(func(a *boot.App) error {
        cfg := a.GetConfig()
        metrics.Init(cfg.Server.Name)
        a.Gin.Use(metrics.GinMiddleware())

        // 一行注册全部中间件（Trace + Recovery + ErrorHandler + Logger + CORS + RateLimit）
        mid.Default(a.Gin, mid.DefaultConfig{
            CORS:        mid.CORSCfg{Enable: true, Mode: "allow-all"},
            AccessLimit: mid.AccessLimitCfg{Enable: true, Total: 300, Duration: 5},
        })

        a.Gin.GET("/health", func(c *gin.Context) {
            c.JSON(http.StatusOK, gin.H{"status": "ok"})
        })
        a.Gin.GET("/metrics", metrics.Handler())
        a.Gin.GET("/ping", func(c *gin.Context) {
            kitlog.InfoContext(c.Request.Context(), "ping", "ip", c.ClientIP())
            resp.Ok(c, "pong")
        })
        return nil
    }); err != nil {
        log.Fatal(err)
    }
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
api/        Proto 服务契约（Buf 管理）
cmd/dilu/    服务/资源脚手架与生成入口
notify/     通用事件推送
metrics/    Prometheus HTTP/gRPC 指标
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

// JWT 认证：token 必须包含 exp；workspace_id/wid 使用整数或十进制字符串
auth := r.Group("/v1").Use(mid.JWT(mid.JWTConfig{
    Secret: jwtSecret,
    Issuer: "auth-service",
    Audience: []string{"my-service"},
}))
uid := mid.GetUID(c)
workspaceID := mid.GetWorkspaceID(c)
tenantID := mid.GetTenantID(c)
shopIDs := mid.GetShopIDs(c)
scopes := mid.GetScopes(c)
nickname := mid.GetNickname(c)

// 仅在可信网关已剥离外部身份头时开启 HeaderUID 信任模式
r.Group("/internal").Use(mid.JWT(mid.JWTConfig{
    HeaderUID:      "x-user-id",
    HeaderTenantID: "x-tenant-id",
    HeaderWorkspaceID: "x-workspace-id",
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
    RequireTLS: true,
    RetryMaxAttempts: 3,
    RetryMethods: []string{"/user.UserService/Get"}, // 只列幂等方法
})
```

### 基础设施传输安全

Redis、etcd/Consul 和 gRPC 服务端的 TLS 都是显式配置项。本地开发可保持默认明文，
不需要准备证书；跨主机、跨网段或生产环境建议在本地配置中设置
`security.requireTLS: true`，此时只要启用的 Redis、注册中心或 gRPC 仍为明文，
`boot.New` 会在建立连接前直接失败。远程配置不能热修改这项启动策略，修改后必须重启服务。

```yaml
security:
  requireTLS: false # 本地/可信内网；生产改为 true

redis:
  addr: "127.0.0.1:6379"
  tls:
    enable: false

grpc:
  enable: false
  tls:
    enable: false
```

生产示例（证书路径和密码通过部署系统或环境变量管理）：

```yaml
security:
  requireTLS: true

redis:
  addr: "redis.internal:6379"
  tls:
    enable: true
    caFile: "/etc/tls/redis-ca.crt"
    serverName: "redis.internal"

registry:
  enable: true
  tls:
    enable: true
    caFile: "/etc/tls/registry-ca.crt"

grpc:
  enable: true
  addr: ":9090"
  tls:
    enable: true
    certFile: "/etc/tls/server.crt"
    keyFile: "/etc/tls/server.key"
    caFile: "/etc/tls/client-ca.crt"
    requireClientCert: true
```

`grpcx.DialOption.RequireTLS` 可对单个 gRPC 客户端调用强制拒绝明文凭据；不设置时保持
兼容旧代码的明文默认值。`InsecureSkipVerify` 在强制 TLS 模式下会被拒绝。

按服务名拨号时，可直接接入注册中心 Resolver。它会持续同步健康实例，并使用 gRPC
`round_robin` 在实例间分配请求：

```go
conn, err := grpcx.DialService(ctx, app.Registry, "inventory-service", grpcx.DialOption{
    Version: "v2", // 可选：只连接 v2 实例
    Metadata: map[string]string{"region": "cn-east-1"}, // 可选标签过滤
    TLSConfig: &tls.Config{ServerName: "inventory.internal"},
    RequireTLS: true,
})
```

直接地址拨号仍使用 `grpcx.Dial`，不会改变旧服务行为。

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

HTTP 出站调用可统一使用 `clientx.HTTPClient`：默认只对幂等方法重试，并可接入熔断和 OTel。

```go
httpc := clientx.NewHTTPClient(clientx.HTTPClientConfig{
    Timeout: 5 * time.Second,
    Breaker: breaker,
    Telemetry: app.Telemetry,
    ServiceName: "inventory-service",
})
req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
resp, err := httpc.Do(ctx, req)
```

注册中心客户端可使用 Resolver 自动接收实例变更：

```go
resolver, _ := registry.NewResolver(ctx, registry.ResolverConfig{
    Registry: app.Registry, Service: "inventory-service", Version: "v2",
    Metadata: map[string]string{"region": "cn-east-1"},
})
defer resolver.Close()
instance, err := resolver.Resolve()
```

## 标准错误码

```go
resp.Fail(c, resp.CodeConflict, "冲突") // 兼容旧协议：HTTP 200 + 业务错误码
resp.FailStatus(c, http.StatusUnauthorized, resp.CodeUnauthorized, "未登录")
```

统一响应固定包含 `code` 和 `msg`。`data` 使用 `omitempty`，无数据时字段不存在；
客户端应把它作为可选字段处理。

## 指标与健康检查

```go
metrics.Init(cfg.Server.Name)
r.Use(metrics.GinMiddleware())
r.GET("/health", func(c *gin.Context) {
    c.JSON(http.StatusOK, gin.H{"status": "ok"})
})
r.GET("/ready", func(c *gin.Context) {
    c.JSON(http.StatusOK, gin.H{"status": "ok"})
})
r.GET("/metrics", metrics.Handler())
```

`boot.New` 会用 `server.name` 初始化服务名，并自动给 boot 创建的 gRPC server 挂载指标。
独立使用 metrics 包时，`metrics.Init` 可在中间件构造前后调用，也可再次调用；后续观测
使用最新服务名。
`/health` 表示进程存活，`/ready` 表示实例可接收流量。两者和 `/metrics`
应位于 JWT 业务路由之外，并通过网络策略限制指标端点访问。应用未显式注册
`/health` 或 `/ready` 时，`boot.Run` 会补默认 OK 路由；需要依赖检查时自行注册
更严格的 `/ready`。
`metrics.Handler()` 默认仅允许回环地址抓取；跨网络 Prometheus 请设置
`METRICS_TOKEN` 或 `METRICS_ALLOWED_CIDRS`。
Consul 服务健康检查默认使用 HTTP，与注册中心自身 TLS 独立；只有服务的 `/ready`
本身启用 HTTPS 时才设置 `registry.checkTLS: true`。pprof 独立管理端口支持 HTTPS/mTLS，
主业务 HTTP 监听仍建议由网关负责 TLS 终止。
依赖检查结果默认缓存 2 秒，可通过 `app.SetReadinessCacheTTL` 调整；传入负值可关闭缓存。

## OpenTelemetry

通过配置或 `OTEL_*` 环境变量启用 OTLP tracing；未配置 exporter 时默认 no-op，不影响旧服务：

```yaml
telemetry:
  enabled: true
  endpoint: "http://127.0.0.1:4318/v1/traces"
  sampleRatio: 0.1
  # sampleRatioSet: true    # 需要关闭采样时设 sampleRatio: 0 并显式打开此项
```

`sampleRatio` 范围为 `0~1`，默认 `0.1`（10% 采样）。开发环境可显式设置为 `1.0`；若要关闭采样，
设置 `sampleRatio: 0` 并同时设置 `sampleRatioSet: true`。
也可使用标准环境变量：
`OTEL_TRACES_SAMPLER=always_off|always_on|parentbased_traceidratio`，并通过
`OTEL_TRACES_SAMPLER_ARG` 设置比例，例如 `0.1`。

`boot.New` 会自动给 Gin 和 boot 创建的 gRPC server 添加 tracing。自建 gRPC client
可传入 `grpcx.DialOption{Telemetry: app.Telemetry}`。

通过 `boot.New` 创建的 DB 和 Redis 客户端在 OTel 开启后也会自动注册基础埋点；
Redis Stream 操作可将 provider 放入上下文：

```go
ctx = stream.WithTelemetry(ctx, app.Telemetry)
id, err := stream.Publish(ctx, app.Redis, "events", payload)
```

消费者处理消息时可恢复发布端上下文：

```go
msgCtx := stream.ContextForMessage(ctx, msg)
```

## 运行时诊断

需要排查 goroutine、heap 或 mutex 时，显式注册 pprof；务必限制在内网或管理端口：

```yaml
diagnostics:
  pprof:
    enabled: false
    prefix: "/debug/pprof"
    addr: "127.0.0.1:6060"
```

配置 `addr` 后由 `boot` 启动独立管理端口；不配置 `addr` 时才注册到业务 Gin 端口。
生产环境建议始终使用 loopback/内网管理地址或额外认证策略保护。

## 事件通知

```go
import "github.com/baowk/dilu-go-kit/notify"

_ = notify.InitConfig(notify.Config{BaseURL: "http://mf-ws:9020", Token: token})
notify.Send("env", map[string]any{"action": "created", "env_id": 123, "workspace_id": 1})
err := notify.SendContextE(ctx, "proxy", payload) // 需要确认送达时使用
notify.SendContext(ctx, "proxy", payload)  // 自动携带 traceId
```

## Redis Stream

```go
import "github.com/baowk/dilu-go-kit/stream"

id, err := stream.Publish(ctx, app.Redis, "sync.tasks", map[string]any{
    "task_id": "123",
    "type": "order_pull",
})

err = stream.RunGroupConsumer(ctx, app.Redis, stream.GroupConsumerConfig{
    Name: "sync-worker.tasks", Stream: "sync.tasks",
    Group: "sync-worker", Consumer: "worker-1",
}, func(ctx context.Context, msgs []stream.Message) {
    for _, msg := range msgs {
        // handle message idempotently before ACK
        _ = stream.Ack(ctx, app.Redis, msg.Stream, "sync-worker", msg.ID)
    }
})
```

`RunGroupConsumer` 会创建消费组，统一处理新消息和 stale pending 消息，并在 Redis 故障时执行带抖动的指数退避、错误聚合和恢复日志。`ReadGroup`、`ClaimStale` 保持一次性原语语义，仅在调用方需要自行编排循环时使用。

## 数据库迁移

推荐每个服务独立维护 `migrations/`：

```text
services/order-service/
  migrations/
    <version>_init.up.sql
    <version>_init.down.sql
```

```bash
go run github.com/baowk/dilu-go-kit/cmd/migrate -dir services/order-service/migrations create -name init
DATABASE_DSN='postgres://user:pass@127.0.0.1:5432/order_db?sslmode=disable' \
  go run github.com/baowk/dilu-go-kit/cmd/migrate -dir services/order-service/migrations up
```

迁移版本是严格递增的十进制整数。`migrate create` 使用 UTC UnixNano，并保证同一进程内
并发创建时唯一递增。已有历史可能使用日期格式版本；不要切换到位数更短、数值更小的格式。

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
  checkType: http              # boot 默认 http；可显式改为 ttl
  checkPath: "/ready"           # Consul HTTP readiness check
  deregisterCriticalAfter: 300  # critical 后 5 分钟自动摘除，建议 300-600 秒
```

服务启动自动注册，关闭自动注销。`boot` 中的 Consul 默认检查实例
`/ready`，避免把存活检查和就绪检查混用。独立使用 `registry.NewConsul`
时为了兼容已有服务，`checkType` 默认仍为 `ttl`。

网关应使用 `registry.WatchUpstreams` 实时发现变更。healthy 实例临时为空时，
它会保留 last known good upstreams 并标记 `stale=true`；默认 30 秒后
发布空路由，不会永久转发到已下线实例。需要调整时使用
`registry.WatchUpstreamsWithOptions`。

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

运行时自动 watch。只有 `jwt/cors/accessLimit/notify` 等动态字段允许发布，并由
`OnConfigApplied` 回调应用到运行中组件；
`server/log/database/redis/grpc/registry` 属于启动字段，变更会被拒绝并要求重启：

```go
app.OnConfigChange(func(cfg *boot.Config) error {
	return validateDynamicConfig(cfg) // 这里只做校验，不产生副作用
})
app.OnConfigApplied(func(cfg *boot.Config) { applyDynamicConfig(cfg) })
```

同一服务多实例可用节点级配置区分：

```bash
REMOTE_NODE=node-1 SERVER_ADDR=:7801 MF_ADVERTISE_IP=10.0.1.5 ./my-service
REMOTE_NODE=node-2 SERVER_ADDR=:7802 MF_ADVERTISE_IP=10.0.1.6 ./my-service
```

对应 KV：

```text
/config/mf-user        # 服务级共享配置
/config/mf-user/node-1 # node-1 覆盖
/config/mf-user/node-2 # node-2 覆盖
```

## 敏感配置

密码、token、JWT secret 不写入 YAML 或远程配置。release/production 模式会拒绝
YAML 内联敏感值，远程配置在所有模式下都会拒绝 DSN/password/secret/token；使用环境变量注入：

```bash
DATABASE_DSN='host=... user=... password=... dbname=...'
DATABASE_MAIN_DSN='host=... user=... password=... dbname=...'
REDIS_PASSWORD='...'
JWT_SECRET='...'
REGISTRY_TOKEN='...'
NOTIFY_WS_URL='http://mf-ws:9020'
NOTIFY_TOKEN='...'
REMOTE_NODE='node-1'
REQUIRE_TLS='true'             # 跨网络/生产强制 Redis、注册中心、gRPC 使用 TLS
METRICS_TOKEN='...'            # 或使用 METRICS_ALLOWED_CIDRS 限制抓取来源
```

`DATABASE_DSN` 会覆盖所有 `database.*.dsn`，只适合单库或所有库共用同一 DSN。
多库服务不要设置它，应使用 `DATABASE_<NAME>_DSN`（如 `DATABASE_MAIN_DSN`、
`DATABASE_AUDIT_DSN`）逐库注入。

Redis、etcd 和 Consul 跨主机部署时应启用 TLS。配置支持 `tls.enable`、`tls.caFile`、
`tls.certFile`、`tls.keyFile` 和 `tls.serverName`；不配置 TLS 仅适合可信内网。

## 配置示例

```yaml
server:
  name: my-service
  version: v1.2.0                # 可选；用于服务发现版本过滤
  addr: ":8080"                  # 监听地址
  # advertiseAddr: "10.0.1.5:8080" # 注册发现地址；空时从 addr 推断
  mode: debug
  readHeaderTimeout: 5
  readTimeout: 15
  writeTimeout: 30
  idleTimeout: 60
  shutdownTimeout: 10
  maxHeaderBytes: 1048576
  maxBodyBytes: 10485760       # 请求体上限，默认 10 MiB
  trustedProxies: ["10.0.0.0/8"] # 仅填写可信网关 CIDR

security:
  requireTLS: false             # 本地/可信内网；跨网络或生产建议设为 true

database:
  main:
    dsn: ""                    # 建议用 DATABASE_MAIN_DSN / DATABASE_DSN 注入
    slowThreshold: 200
    pingOnOpen: true
    prepareStmt: true

redis:
  addr: "127.0.0.1:6379"
  username: ""                # Redis 6+ ACL（可选）
  password: ""                # 建议用 REDIS_PASSWORD 注入
  # tls:
  #   enable: true
  #   caFile: "/etc/tls/redis-ca.crt"
  #   serverName: "redis.internal"

grpc:
  enable: false
  # addr: ":9090"
  # tls:
  #   enable: true
  #   certFile: "/etc/tls/server.crt"
  #   keyFile: "/etc/tls/server.key"
  #   caFile: "/etc/tls/client-ca.crt"
  #   requireClientCert: true

log:
  output: console             # console / file / both
  # file:
  #   path: "logs/app.log"

jwt:
  secret: ""                  # 建议用 JWT_SECRET 注入
  expires: 1440
  issuer: auth-service
  audience: ["my-service"]
  # trustHeaderUid: true 时限制来源网段（逗号分隔）
  # trustedHeaderCidrs: "10.0.0.0/8,127.0.0.1/32"
  # allowQueryToken: false     # WebSocket URL token 兼容开关，默认关闭

diagnostics:
  pprof:
    enabled: false
    # addr: "127.0.0.1:6060"
    # authToken: ""             # 使用 PPROF_TOKEN 注入
    # allowedCidrs: "127.0.0.1/32"

cors:
  enable: true
  mode: allow-all

accessLimit:
  enable: true
  total: 300
  duration: 5
  backend: redis
  keyPrefix: "my-service:ratelimit"

notify:
  wsUrl: "http://mf-ws:9020"
  token: ""                    # 建议用 NOTIFY_TOKEN 注入
  timeout: 3                   # 单次请求超时（秒）
  maxPayloadBytes: 1048576    # 单次事件上限，最大 16 MiB

registry:
  enable: true
  type: etcd                  # etcd / consul
  endpoints: ["127.0.0.1:2379"]
  dialTimeout: 5
  checkType: http            # boot 中 consul 默认 http，可改为 ttl
  checkPath: "/ready"         # consul readiness check path
  deregisterCriticalAfter: 300 # consul critical 后自动摘除延迟，建议 300-600 秒
  # address: "127.0.0.1:8500"  # consul
  # tls:                       # 同时用于服务注册和远程配置
  #   enable: true
  #   caFile: "/etc/tls/registry-ca.crt"
  #   serverName: "registry.internal"
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
