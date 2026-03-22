# [项目名] — AI 开发约定

> 本项目基于 [dilu-go-kit](https://github.com/baowk/dilu-go-kit) 开发，遵循其开发规范。

## 项目结构

- `cmd/` — 入口
- `internal/modules/` — 业务模块
- `resources/` — 配置文件

## 技术栈

- 框架：dilu-go-kit（Gin + GORM + etcd）
- 数据库：PostgreSQL
- 缓存：Redis
- 服务发现：etcd

## 数据访问层（重要）

每个业务模块的目录结构：

```
internal/modules/{module}/
  model/    ← 手写结构体（gorm tag，一张表一个文件，必须有 TableName()）
  store/    ← Store 接口 + PG 实现 + Init(db)/S()
  service/  ← 业务逻辑（只通过 store 接口访问数据，禁止直接用 gorm.DB）
  apis/     ← HTTP handler（用 resp.Ok/Fail/Page 返回）
  router/   ← 路由注册
```

**禁止**：
- 禁止 service 层直接使用 `gorm.DB`
- 禁止在代码中硬编码 Redis key
- 分区表查询必须带分区键

## API 规范

- URL：`/{version}/{module}/{resource}`
- 响应：`{"code": 200, "msg": "OK", "data": {...}}`
- 分页：`page` 从 1 开始，`size` 默认 20 最大 500
- 错误码：200 成功 / 400xx 参数 / 401xx 认证 / 403xx 权限 / 500xx 服务端

## 中间件

```go
import "github.com/baowk/dilu-go-kit/mid"
import "github.com/baowk/dilu-go-kit/resp"

r.Use(mid.Recovery(), mid.CORS())
auth := r.Group("/v1/xxx").Use(mid.JWT(mid.JWTConfig{Secret: "..."}))
uid := mid.GetUID(c)
resp.Ok(c, data)
resp.Fail(c, 40101, "未登录")
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
    store.Init(a.DB("main"))
    a.Gin.Use(mid.Recovery(), mid.CORS())
    router.Init(a.Gin)
    return nil
})
```

## 协作偏好

- 先出计划让用户确认，确认后全程自主执行
- 修改架构相关代码时同步更新文档
- 读表结构看 `model/` 或 SQL schema，不读 gen 文件
