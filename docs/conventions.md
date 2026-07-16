# 开发规范

## 一、项目结构

```
my-service/
  cmd/main.go                       <- 入口
  migrations/                       <- SQL migrations，一组 up/down 文件
  internal/{module}/
    model/                          <- 手写结构体（一张表一个文件）
      task.go
      task_comment.go
    store/                          <- Store 接口 + PG 实现 + Init/S()
      store.go                      <- 只放 Stores 聚合、Init、S
      task_pg.go                    <- task 表 PG 实现
      task_comment_pg.go            <- task_comment 表 PG 实现
    service/                        <- 业务逻辑（一类资源一个文件）
      task.go
      task_comment.go
      dto/                          <- 请求/响应 DTO（一类资源一个文件）
        task.go
        task_comment.go
    apis/                           <- HTTP handler（一类资源一个文件，调用 resp 包）
      task_api.go
      task_comment_api.go
    grpc/                           <- gRPC handler（可选）
    router/                         <- 路由注册
  internal/common/                  <- 服务内公共代码
    config/                         <- 自定义配置扩展
    middleware/                     <- 服务特有中间件
  resources/config.dev.yaml         <- 配置文件
  go.mod
```

## 二、数据访问层

### 数据库迁移

每个服务必须独立维护自己的 `migrations/` 目录，不共享其他服务迁移。

命名：

```text
migrations/
  20260709120000_init.up.sql
  20260709120000_init.down.sql
  20260709121000_add_order_status.up.sql
  20260709121000_add_order_status.down.sql
```

命令：

```bash
go run github.com/baowk/dilu-go-kit/cmd/migrate -dir migrations create -name init
DATABASE_DSN='postgres://user:pass@127.0.0.1:5432/order_db?sslmode=disable' \
  go run github.com/baowk/dilu-go-kit/cmd/migrate -dir migrations up
```

规则：
- 所有 DDL 通过 migration 进入仓库，不允许只依赖 GORM AutoMigrate。
- 每个 `.up.sql` 必须有对应 `.down.sql`。
- 生产环境执行前必须先在 staging 验证。
- dirty 状态只能由负责人确认后使用 `force` 修复。

### 文件拆分硬约束

AI 生成或补充代码时必须按“表/资源”为最小文件边界，避免把一个模块所有 model、store、service、dto、handler 都塞进单个大文件。

**必须**：
- 每张表一个 `model/{table}.go`，文件内只放该表的结构体、`TableName()` 和该表强相关的小常量/枚举。
- 每张表一个 `store/{table}_pg.go`，文件内只放该表对应的 PG store 实现。
- `store/store.go` 只放 Store 接口聚合、`Stores`、`Init(db)`、`S()`，不要放具体 SQL 实现。
- 每类资源一个 `service/{resource}.go`，只放该资源业务逻辑。
- 每类资源一个 `service/dto/{resource}.go`，只放该资源请求/响应 DTO。
- 每类资源一个 `apis/{resource}_api.go`，只放该资源 HTTP handler。
- 新增表/资源时新增整组文件，不扩写已有资源文件；例如新增 `task_comment` 时创建 `model/task_comment.go`、`store/task_comment_pg.go`、`service/task_comment.go`、`service/dto/task_comment.go`、`apis/task_comment_api.go`。

**禁止**：
- 禁止创建或扩写 `model/model.go` 来集中放多个表结构体。
- 禁止创建或扩写 `store/pg.go`、`store/store_pg.go`、`store/repository.go` 来集中放多个表的查询实现。
- 禁止创建或扩写 `service/service.go`、`service/dto/dto.go`、`apis/apis.go` 来集中放多个资源的业务逻辑、DTO 或 handler。
- 禁止把多个表的 CRUD 混在一个 store struct 里；每张表一个 `XxxStore` 接口和一个 `pgXxxStore` 实现。
- 单文件超过约 300 行时必须优先拆分到按表/按职责文件，而不是继续追加。

### Model

```go
package model

import "time"

type Task struct {
    ID        int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
    Title     string    `gorm:"column:title;size:200" json:"title"`
    Status    int16     `gorm:"column:status;default:1" json:"status"`
    CreatedAt time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
    UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (Task) TableName() string { return "task" }
```

**规则**：
- 一张表一个文件，命名使用 snake_case 表名，如 `task.go`、`task_comment.go`
- 字段类型与 DDL 严格对齐
- `*time.Time` 可空字段，`time.Time` 非空字段
- 必须有 `TableName()` 方法

### Store 接口

```go
type TaskStore interface {
    GetByID(ctx context.Context, id int64) (*model.Task, error)
    List(ctx context.Context, opts store.ListOpts) ([]*model.Task, int64, error)
    Create(ctx context.Context, t *model.Task) error
    Update(ctx context.Context, id int64, updates map[string]any) (int64, error)
    Delete(ctx context.Context, id int64) (int64, error)
}
```

**规则**：
- 所有方法第一个参数 `context.Context`
- 分区表查询必须带分区键（如 `workspace_id`）
- 部分更新用 `map[string]any`
- 返回 `(int64, error)` 的方法，int64 是 RowsAffected
- 每个 Store 接口和 PG 实现只服务一张表；跨表事务放 service 层编排
- 禁止 service 层直接使用 `gorm.DB`

### Store 初始化

```go
func Init(db *gorm.DB) { ... }  // 启动时调用一次
func S() *Stores { ... }        // 获取全局实例
```

## 三、API 规范

### URL

```
/{version}/{module}/{resource}
/{version}/{module}/{resource}/{id}
/{version}/{module}/{resource}/{id}/{action}
```

**HTTP 方法**：
- `GET` — 查询（列表/详情）
- `POST` — 创建 / 批量操作 / 动作
- `PUT` — 更新
- `DELETE` — 删除

### 响应格式

```json
// 成功
{"code": 200, "msg": "OK", "data": {...}}

// 失败
{"code": 40001, "msg": "参数错误"}

// 分页
{"code": 200, "msg": "OK", "data": {"list": [], "total": 100, "pageSize": 20, "currentPage": 1}}
```

### 错误码

```
200       成功
400xx     参数错误（40001 缺少字段、40002 格式错误）
401xx     认证错误（40101 未登录、40102 Token 过期、40103 Token 无效）
403xx     权限错误（40301 无权操作）
404xx     资源不存在
409xx     冲突（40901 资源已存在）
429xx     限流（42901 请求过于频繁）
500xx     服务端错误（50001 数据库错误、50002 外部服务不可用）
```

### 分页参数

```
page — 页码，从 1 开始，默认 1
size — 每页条数，默认 20，最大 500
```

## 四、中间件

```go
import (
    "github.com/baowk/dilu-go-kit/mid"
    "github.com/baowk/dilu-go-kit/log"
)

// 方式一：一行注册全部（推荐）
mid.Default(a.Gin, mid.DefaultConfig{
    CORS:        mid.CORSCfg{Enable: true, Mode: "allow-all"},
    AccessLimit: mid.AccessLimitCfg{Enable: true, Total: 300, Duration: 5},
})
// 注册顺序：Trace → Recovery → ErrorHandler → Logger → CORS → RateLimit

// 方式二：单独使用
r.Use(mid.Trace())          // traceId 生成/传递（X-Trace-Id；X-Request-Id 仅作兼容别名）
r.Use(mid.Recovery())       // panic 恢复
r.Use(mid.ErrorHandler())   // AppError panic 捕获
r.Use(mid.Logger())         // 请求日志（method/path/status/latency/traceId）
r.Use(mid.CORS())           // CORS（支持 whitelist）
r.Use(mid.RateLimit(100, time.Minute))
r.Use(mid.RateLimitFromConfig(mid.AccessLimitCfg{
    Enable: true,
    Total: 300,
    Duration: 5,
    Backend: "redis",              // memory（默认）或 redis
    Redis: app.Redis,              // nil 时自动回退 memory
    KeyPrefix: "gateway:ratelimit",
}))
limiter := mid.NewRateLimiter(100, time.Minute) // 需要显式生命周期时
r.Use(limiter.Middleware())
// app.OnClose(limiter.Close)

// JWT 认证
auth := r.Group("/v1/xxx").Use(mid.JWT(mid.JWTConfig{
    Secret: jwtSecret,
    Issuer: "auth-service",
    Audience: []string{"my-service"},
}))
// token 必须包含 exp；uid/workspace_id 可安全使用 int64

// 仅在可信网关已剥离外部身份头时开启 HeaderUID 信任模式
r.Group("/internal").Use(mid.JWT(mid.JWTConfig{
    HeaderUID:      "x-user-id",
    HeaderTenantID: "x-tenant-id",
    HeaderWorkspaceID: "x-workspace-id",
    HeaderShopIDs:  "x-shop-ids",
    HeaderScopes:   "x-scopes",
    TrustHeaderUID: true,
}))

// 获取用户信息
uid := mid.GetUID(c)            // int64
tenantID := mid.GetTenantID(c)  // int64
workspaceID := mid.GetWorkspaceID(c) // int64
shopIDs := mid.GetShopIDs(c)    // []int64
scopes := mid.GetScopes(c)      // []string
nickname := mid.GetNickname(c)  // string
roleID := mid.GetRoleID(c)     // int
phone := mid.GetPhone(c)       // string

// gRPC traceId 透传
conn, _ := grpcx.Dial(addr, grpcx.DialOption{
    RetryMaxAttempts: 3,
    RetryMethods: []string{"/package.QueryService/Get"}, // 仅限幂等方法
})
```

### 服务客户端

```go
import "github.com/baowk/dilu-go-kit/clientx"

breaker := clientx.NewBreaker(clientx.BreakerConfig{
    FailureThreshold: 5,
    Cooldown: 30 * time.Second,
})

err := breaker.Do(ctx, func(ctx context.Context) error {
    return clientx.Do(ctx, clientx.RetryConfig{MaxAttempts: 3}, func(ctx context.Context) error {
        // 调用下游服务
        return nil
    })
})
if err != nil {
    err = clientx.MapGRPCError(err)
}
```

**规则**：
- 调用下游服务必须设置超时，由调用方 context 控制。
- 可重试操作必须保证幂等。
- gRPC 默认不重试；开启时必须逐个列出幂等方法。
- 熔断打开时应返回 `resp.CodeServiceDown` 或进入异常处理。
- gRPC 错误返回前统一用 `clientx.MapGRPCError` 映射。

### 日志

```go
import "github.com/baowk/dilu-go-kit/log"

log.Info("msg", "key", value)                        // 基础
log.InfoContext(ctx, "msg", "key", value)             // 自动带 trace_id
log.With("module", "auth").Error("failed", "err", e) // 子 logger
```

输出模式通过 `log.output` 配置：`console`（默认）、`file`（仅文件）、`both`（双写）。
文件输出使用 lumberjack 自动 rotation，详见配置示例。

### 事件通知

```go
import "github.com/baowk/dilu-go-kit/notify"

_ = notify.InitConfig(notify.Config{BaseURL: "http://mf-ws:9020", Token: token})
notify.Send("env", map[string]any{"action": "created", "env_id": 123})
notify.SendContext(ctx, "proxy", payload)  // 携带 traceId，兼容写入 X-Request-Id 同值别名
err := notify.SendContextE(ctx, "proxy", payload) // 关键通知必须处理错误
```

### Redis Stream

```go
import "github.com/baowk/dilu-go-kit/stream"

_ = stream.EnsureGroup(ctx, app.Redis, "sync.tasks", "sync-worker", "0")

_, err := stream.Publish(ctx, app.Redis, "sync.tasks", map[string]any{
    "task_id": "123",
    "type": "order_pull",
})

msgs, err := stream.ReadGroup(ctx, app.Redis, stream.ReaderConfig{
    Stream:   "sync.tasks",
    Group:    "sync-worker",
    Consumer: "worker-1",
})
stale, next, err := stream.ClaimStale(ctx, app.Redis, stream.ClaimConfig{
    Stream: "sync.tasks", Group: "sync-worker", Consumer: "worker-1",
    MinIdle: time.Minute, Start: "0-0",
})
_ = stale
_ = next
for _, msg := range msgs {
    // 消费端必须先检查业务幂等键
    _ = stream.Ack(ctx, app.Redis, msg.Stream, "sync-worker", msg.ID)
}
```

## 五、配置

### 完整配置示例

```yaml
server:
  name: my-service
  addr: ":8080"                # 监听地址
  # advertiseAddr: "10.0.1.5:8080" # 注册发现地址；空时从 addr 推断
  mode: debug             # debug / release
  readHeaderTimeout: 5
  readTimeout: 15
  writeTimeout: 30
  idleTimeout: 60
  shutdownTimeout: 10
  maxHeaderBytes: 1048576
  trustedProxies: []      # 仅配置可信反向代理 CIDR

log:
  output: console           # console（默认）/ file / both
  file:                     # output 为 file 或 both 时必填
    path: "logs/app.log"
    maxSize: 100            # MB/文件（默认 100）
    maxAge: 7               # 保留天数（默认 7）
    maxBackups: 5           # 旧文件数（默认 5）
    compress: false         # gzip 压缩

database:
  main:
    dsn: ""                 # 建议用 DATABASE_MAIN_DSN / DATABASE_DSN 注入
    maxIdle: 10           # 最大空闲连接数（默认 10）
    maxOpen: 50           # 最大打开连接数（默认 50）
    maxLifetime: 3600     # 连接最大存活时间，秒（默认 3600）
    maxIdleTime: 300      # 空闲连接回收时间，秒（默认 300）
    slowThreshold: 200    # 慢查询阈值，ms（默认 200，超过自动告警）
    pingOnOpen: true      # 启动时探活（默认 true）
    prepareStmt: true     # PgBouncer transaction 模式下可关闭

redis:
  addr: "127.0.0.1:6379"
  username: ""              # Redis 6+ ACL 用户名（可选）
  password: ""              # 建议用 REDIS_PASSWORD 注入
  db: 0

grpc:
  enable: false
  addr: ":9090"              # 监听地址
  # advertiseAddr: "10.0.1.5:9090" # 注册发现地址；空时从 addr 推断

jwt:
  secret: ""                # 建议用 JWT_SECRET 注入
  expires: 1440           # 过期时间，分钟
  refresh: 30             # 自动刷新窗口，分钟
  issuer: auth-service
  audience: ["my-service"]

cors:
  enable: true
  mode: allow-all         # allow-all / whitelist
  whitelist:              # mode=whitelist 时生效
    - "https://example.com"

accessLimit:
  enable: true
  total: 300              # 每窗口最大请求数
  duration: 5             # 窗口时长，秒
  backend: redis          # 多实例必须使用 redis
  keyPrefix: "my-service:ratelimit"

notify:
  wsUrl: "http://mf-ws:9020"  # WebSocket 网关通知地址
  token: ""                    # 使用 NOTIFY_TOKEN 注入

registry:
  enable: true
  type: etcd                # etcd（默认）/ consul
  endpoints:                # etcd 端点
    - "127.0.0.1:2379"
  # address: "127.0.0.1:8500"  # consul 地址
  # token: ""                   # consul ACL token，建议用 REGISTRY_TOKEN 注入
  prefix: "/services/"
  ttl: 30
  dialTimeout: 5
  configKey: "/config/"     # 有值即启用远程配置（自动拼 server.name）
  # configNode: "node-1"   # 节点级覆盖（可选，或 env REMOTE_NODE）
  # configFormat: yaml      # yaml（默认）/ json
```

### 扩展配置

嵌入 `boot.Config` 或用 `boot.LoadConfig(path, &myConfig)` 加载自定义结构：

```go
type MyConfig struct {
    boot.Config `mapstructure:",squash"`
    Custom struct {
        APIKey string `mapstructure:"apiKey"`
    } `mapstructure:"custom"`
}
```

## 六、服务注册与发现

支持 etcd 和 consul 两种后端，启用 `registry` 后服务启动自动注册，关闭自动注销。

**注册格式**：
```
key:   /{prefix}/{service_name}/{instance_id}
value: {"name":"mf-user","instance_id":"mf-user-host-1234-56789","addr":"10.0.1.5:7801","grpc_addr":"10.0.1.5:7889"}
lease: 30s TTL + keepalive
```

**网关侧**：Watch 前缀，动态更新路由表，新服务上线/下线无需改配置。

**本地开发**：`registry.enable: false` 即可关闭，使用静态地址。

**注册地址**：`server.addr` / `grpc.addr` 是监听地址，`advertiseAddr` 是注册给其他服务连接的地址。多机或容器部署建议显式配置 `server.advertiseAddr` / `grpc.advertiseAddr`，也可用 `SERVER_ADVERTISE_ADDR` / `GRPC_ADVERTISE_ADDR` 注入。

## 七、远程配置

复用 registry 的 etcd/consul 连接，从 KV 加载配置并实时热更新。

### 启用

在 `registry` 中设置 `configKey` 即可，无需额外配置段：

```yaml
registry:
  enable: true
  type: etcd
  endpoints: ["127.0.0.1:2379"]
  configKey: "/config/"         # 有值即启用
  configNode: "node-1"          # 可选，或 env REMOTE_NODE
```

### 合并规则

三层深度合并（每层只覆盖它包含的 key，不清零其他字段）：

```
1. 本地 YAML           ← 基础配置
2. /config/mf-user     ← 服务级共享（所有 mf-user 节点共用）
3. /config/mf-user/node-1  ← 节点级覆盖（仅该节点生效）
```

### 热更新

运行时自动 watch 远程 key（etcd 实时推送，consul long-poll）。仅
`jwt/cors/accessLimit/notify` 等动态字段允许发布，并由 `OnConfigApplied` 应用；`server/log/database/redis/grpc/registry`
属于启动字段，修改会被拒绝并要求重启。

```go
app.OnConfigChange(func(cfg *boot.Config) error {
    return validateDynamicConfig(cfg) // 校验阶段禁止产生副作用
})
app.OnConfigApplied(func(cfg *boot.Config) { applyDynamicConfig(cfg) })
```

### 多服务 KV 布局示例

```
etcd/consul KV:
  /config/mf-user          → { cors: ..., accessLimit: ... }
  /config/mf-user/node-1   → { notify: ... }
  /config/mf-user/node-2   → { notify: ... }
  /config/mf-order         → { jwt: ..., cors: ... }
  /config/mf-gateway       → { jwt: ..., cors: ... }
```

### 多节点启动示例

同一服务多实例不要复制多份 YAML，可用环境变量区分节点：

```bash
REMOTE_NODE=node-1 SERVER_ADDR=:7801 SERVER_ADVERTISE_ADDR=10.0.1.5:7801 ./mf-user
REMOTE_NODE=node-2 SERVER_ADDR=:7802 SERVER_ADVERTISE_ADDR=10.0.1.6:7802 ./mf-user
```

`REMOTE_NODE=node-1` 会读取 `/config/mf-user/node-1`；`REMOTE_NODE=node-2` 会读取 `/config/mf-user/node-2`。节点级 key 删除后会回落到服务级/本地配置；服务级 key 删除后，热更新会回落到本地配置。

## 八、敏感配置

密码、token、JWT secret 不要提交到 YAML 或远程配置。release/production 模式会拒绝
YAML 内联敏感值，远程配置在所有模式都会拒绝 DSN/password/secret/token。环境变量覆盖优先级最高：

| 环境变量 | 覆盖字段 |
| --- | --- |
| `DATABASE_DSN` | 所有 `database.*.dsn` |
| `DATABASE_<NAME>_DSN` | 指定数据库，如 `DATABASE_MAIN_DSN` |
| `REDIS_USERNAME` / `REDIS_PASSWORD` | `redis.username/password` |
| `JWT_SECRET` | `jwt.secret` |
| `REGISTRY_TOKEN` | `registry.token` |
| `NOTIFY_WS_URL` | `notify.wsUrl` |
| `NOTIFY_TOKEN` | `notify.token` |
| `SERVER_ADDR` / `SERVER_ADVERTISE_ADDR` | `server.addr/advertiseAddr` |
| `GRPC_ADDR` / `GRPC_ADVERTISE_ADDR` | `grpc.addr/advertiseAddr` |
| `REMOTE_NODE` | `registry.configNode` |
