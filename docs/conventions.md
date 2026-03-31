# 开发规范

## 一、项目结构

```
my-service/
  cmd/main.go                       <- 入口
  internal/modules/{module}/
    model/                          <- 手写结构体（一张表一个文件）
    store/                          <- Store 接口 + PG 实现 + Init/S()
    service/                        <- 业务逻辑
      dto/                          <- 请求/响应 DTO
    apis/                           <- HTTP handler（调用 resp 包）
    grpc/                           <- gRPC handler（可选）
    router/                         <- 路由注册
  internal/common/                  <- 服务内公共代码
    config/                         <- 自定义配置扩展
    middleware/                     <- 服务特有中间件
  resources/config.dev.yaml         <- 配置文件
  go.mod
```

## 二、数据访问层

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
- 一张表一个文件
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
r.Use(mid.Trace())          // traceId 生成/传递（X-Trace-Id header）
r.Use(mid.Recovery())       // panic 恢复
r.Use(mid.ErrorHandler())   // AppError panic 捕获
r.Use(mid.Logger())         // 请求日志（method/path/status/latency/traceId）
r.Use(mid.CORS())           // CORS（支持 whitelist）
r.Use(mid.RateLimit(100, time.Minute))

// JWT 认证
auth := r.Group("/v1/xxx").Use(mid.JWT(mid.JWTConfig{
    Secret:    "your-secret",
    HeaderUID: "a_uid",
}))

// 获取用户信息
uid := mid.GetUID(c)            // int64
nickname := mid.GetNickname(c)  // string
roleID := mid.GetRoleID(c)     // int
phone := mid.GetPhone(c)       // string

// gRPC traceId 透传
conn, _ := grpc.NewClient(addr, grpc.WithUnaryInterceptor(mid.GRPCUnaryClientInterceptor()))
```

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

notify.Init("http://mf-ws:9020")
notify.Send("env", map[string]any{"action": "created", "env_id": 123})
notify.SendContext(ctx, "proxy", payload)  // 携带 traceId
```

## 五、配置

### 完整配置示例

```yaml
server:
  name: my-service
  addr: ":8080"
  mode: debug             # debug / release

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
    dsn: "host=127.0.0.1 user=postgres dbname=mydb sslmode=disable"
    maxIdle: 10           # 最大空闲连接数（默认 10）
    maxOpen: 50           # 最大打开连接数（默认 50）
    maxLifetime: 3600     # 连接最大存活时间，秒（默认 3600）
    maxIdleTime: 300      # 空闲连接回收时间，秒（默认 300）
    slowThreshold: 200    # 慢查询阈值，ms（默认 200，超过自动告警）
    pingOnOpen: true      # 启动时探活（默认 true）

redis:
  addr: "127.0.0.1:6379"
  username: ""              # Redis 6+ ACL 用户名（可选）
  password: ""
  db: 0

grpc:
  enable: false
  addr: ":9090"

jwt:
  secret: "your-secret"
  expires: 1440           # 过期时间，分钟
  refresh: 30             # 自动刷新窗口，分钟

cors:
  enable: true
  mode: allow-all         # allow-all / whitelist
  whitelist:              # mode=whitelist 时生效
    - "https://example.com"

accessLimit:
  enable: true
  total: 300              # 每窗口最大请求数
  duration: 5             # 窗口时长，秒

notify:
  wsUrl: "http://mf-ws:9020"  # WebSocket 网关通知地址

registry:
  enable: true
  type: etcd                # etcd（默认）/ consul
  endpoints:                # etcd 端点
    - "127.0.0.1:2379"
  # address: "127.0.0.1:8500"  # consul 地址
  # token: ""                   # consul ACL token
  prefix: "/services/"
  ttl: 30
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
value: {"name":"mf-user","instance_id":"mf-user-host-1234-56789","addr":":7801","grpc_addr":":7889"}
lease: 30s TTL + keepalive
```

**网关侧**：Watch 前缀，动态更新路由表，新服务上线/下线无需改配置。

**本地开发**：`registry.enable: false` 即可关闭，使用静态地址。

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

运行时自动 watch 远程 key，变更秒级生效（etcd 实时推送，consul long-poll）。
业务层通过回调感知变更：

```go
app.OnConfigChange(func(cfg *boot.Config) error {
    log.Info("config updated", "redis", cfg.Redis.Addr)
    return nil  // 返回 error 拒绝此次更新
})
```

### 多服务 KV 布局示例

```
etcd/consul KV:
  /config/mf-user          → { database: ..., redis: ... }
  /config/mf-user/node-1   → { server: { addr: ":7801" } }
  /config/mf-user/node-2   → { server: { addr: ":7802" } }
  /config/mf-order         → { database: ..., redis: ... }
  /config/mf-gateway       → { jwt: ..., cors: ... }
```
