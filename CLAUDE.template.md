# [项目名] — AI 开发约定

> 本项目基于 [dilu-go-kit](https://github.com/baowk/dilu-go-kit) 开发，遵循其开发规范。

## 项目结构

- `cmd/` — 入口
- `internal/` — 业务模块
- `resources/` — 配置文件

## 技术栈

- 框架：dilu-go-kit（Gin + GORM + etcd）
- 数据库：PostgreSQL
- 缓存：Redis
- 服务发现：etcd

## 数据访问层（重要）

每个业务模块的目录结构：

```
internal/{module}/
  model/    ← 手写结构体（gorm tag，一张表一个文件，必须有 TableName()）
  store/    ← Store 接口 + PG 实现 + Init(db)/S()
  service/  ← 业务逻辑（一类资源一个文件，只通过 store 接口访问数据）
    dto/    ← 请求/响应 DTO（一类资源一个文件）
  apis/     ← HTTP handler（一类资源一个文件，用 resp.Ok/Fail/Page 返回）
  router/   ← 路由注册
```

### 文件拆分硬约束

AI 写代码时必须按表/资源拆文件，避免生成巨大的 `model.go` / `store.go` / `service.go` / `apis.go`：

- 每张表一个 `model/{table}.go`，只放该表结构体和 `TableName()`。
- 每张表一个 `store/{table}_pg.go`，只放该表 PG 实现。
- `store/store.go` 只放 Store 聚合、`Init(db)`、`S()`。
- 每类资源一个 `service/{resource}.go`、`service/dto/{resource}.go`、`apis/{resource}_api.go`。
- 新增资源时新增整组文件，例如 `model/task_comment.go` + `store/task_comment_pg.go` + `service/task_comment.go` + `service/dto/task_comment.go` + `apis/task_comment_api.go`。

**禁止**：
- 禁止 service 层直接使用 `gorm.DB`
- 禁止把多个表的 model 集中写进 `model/model.go`
- 禁止把多个表的 PG 查询集中写进 `store/store.go`、`store_pg.go` 或 `repository.go`
- 禁止把多个资源的业务逻辑、DTO、handler 集中写进 `service/service.go`、`service/dto/dto.go` 或 `apis/apis.go`
- 禁止在代码中硬编码 Redis key
- 分区表查询必须带分区键

## API 规范

- URL：`/{version}/{module}/{resource}`
- 响应：`{"code": 200, "msg": "OK", "data": {...}}`
- 分页：`page` 从 1 开始，`size` 默认 20 最大 500
- 错误码：200 成功 / 400xx 参数 / 401xx 认证 / 403xx 权限 / 500xx 服务端

## 中间件

```go
import (
    "github.com/baowk/dilu-go-kit/mid"
    "github.com/baowk/dilu-go-kit/resp"
    "github.com/baowk/dilu-go-kit/log"
    "github.com/baowk/dilu-go-kit/notify"
)

mid.Default(a.Gin, mid.DefaultConfig{...})  // 一行注册全部中间件
auth := r.Group("/v1/xxx").Use(mid.JWT(mid.JWTConfig{
    Secret: jwtSecret,
    Issuer: "auth-service",
    Audience: []string{"my-service"},
})) // token 必须包含 exp 和 workspace_id/wid
uid := mid.GetUID(c)
workspaceID := mid.GetWorkspaceID(c)
resp.Ok(c, data)
resp.FailStatus(c, http.StatusUnauthorized, resp.CodeUnauthorized, "未登录")
log.InfoContext(ctx, "msg", "key", val)  // 自动带 trace_id
notify.Send("env", payload)              // 事件推送
resp.Page(c, list, total, page, size)
```

## 配置

```yaml
server:
  name: my-service
  addr: ":8080"
  mode: debug
database:
  main:
    dsn: "host=127.0.0.1 user=postgres dbname=xxx sslmode=disable"
redis:
  addr: "127.0.0.1:6379"
registry:
  enable: true
  endpoints: ["127.0.0.1:2379"]
```

## 启动模式

```go
app, _ := boot.New("resources/config.dev.yaml")
app.Run(func(a *boot.App) error {
    cfg := a.GetConfig()
    store.Init(a.DB("main"))
    mid.Default(a.Gin, mid.DefaultConfig{
        CORS:        mid.CORSCfg{Enable: true, Mode: "allow-all"},
        AccessLimit: mid.AccessLimitCfg{
            Enable: true, Total: 300, Duration: 5,
            Backend: "redis", Redis: a.Redis, KeyPrefix: "my-service:ratelimit",
        },
    })
    _ = notify.InitConfig(notify.Config{BaseURL: cfg.Notify.WsURL, Token: cfg.Notify.Token})
    router.Init(a.Gin, mid.JWTConfig{
        Secret: cfg.JWT.Secret, Issuer: cfg.JWT.Issuer, Audience: cfg.JWT.Audience,
    })
    return nil
})
```

提交前运行 `go test -race ./...`、`go vet ./...`、`govulncheck ./...` 和 `git diff --check`。

## 协作偏好

- 先出计划让用户确认，确认后全程自主执行
- 修改架构相关代码时同步更新文档
- 读表结构看 `model/` 或 SQL schema，不读 gen 文件
