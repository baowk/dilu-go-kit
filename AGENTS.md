# dilu-go-kit — Codex 项目约定

## 本仓库结构

- `boot/` — 服务启动（Config/Logger/DB/Redis/gRPC/Registry/RemoteConfig）
- `log/` — 统一日志接口（slog + traceId + lumberjack file rotation）
- `resp/` — 统一 HTTP 响应（Ok/Fail/Page/Error/FailStatus）
- `mid/` — 中间件（JWT/CORS/Recovery/RateLimit）
- `store/` — 数据访问基础类型（ListOpts）
- `registry/` — 服务注册与发现（etcd / consul）
- `grpcx/` — gRPC client helper（traceId + keepalive + TLS/custom credentials）
- `example/` — 完整示例服务
- `docs/` — 开发规范 + 快速开始

## 开发约定

- 详细规范见 `docs/conventions.md`
- 快速上手见 `docs/quickstart.md`
- 示例服务使用 `example/internal/{module}/...`，不要再使用 `internal/modules/{module}`。

## 模块拆分硬约束

业务模块按资源拆文件，禁止把多个资源堆到一个大文件：

```text
internal/{module}/
  model/{table}.go
  store/store.go
  store/{table}_pg.go
  service/{resource}.go
  service/dto/{resource}.go
  apis/{resource}_api.go
  router/router.go
```

必须遵守：

- 一张表一个 `model/{table}.go`，只放该表结构体、`TableName()` 和强相关常量。
- 一张表一个 `store/{table}_pg.go`，只放该表 PG 实现。
- `store/store.go` 只放 Store 接口聚合、`Stores`、`Init(db)`、`S()`。
- 一类资源一个 `service/{resource}.go`、`service/dto/{resource}.go`、`apis/{resource}_api.go`。
- 新增资源时新增整组文件，例如 `task_comment` 对应 `model/task_comment.go`、`store/task_comment_pg.go`、`service/task_comment.go`、`service/dto/task_comment.go`、`apis/task_comment_api.go`。

禁止：

- 禁止集中创建 `model/model.go`、`store/store_pg.go`、`service/service.go`、`service/dto/dto.go`、`apis/apis.go`。
- 禁止 service 层直接使用 `gorm.DB`，只能通过 store 接口。
- 分区表查询必须带分区键，如 `workspace_id`。
- 单文件超过约 800 行时优先按表/资源/职责拆分。

## 安全约定

- 密码、token、JWT secret 不写入 YAML 或远程配置，优先用环境变量：`DATABASE_DSN`、`DATABASE_<NAME>_DSN`、`REDIS_PASSWORD`、`JWT_SECRET`、`REGISTRY_TOKEN`、`NOTIFY_TOKEN`。
- JWT 必须校验 `exp`，并按服务配置 issuer/audience；分区服务必须从认证上下文读取 `workspace_id`，禁止信任请求参数。
- 只有在可信网关已剥离外部身份头时才允许 `TrustHeaderUID: true`。
- gRPC client 默认明文只适合可信内网；跨网络使用 `grpcx.DialOption{TLSConfig: ...}` 或自定义 credentials。
- gRPC 默认不重试；开启重试必须通过 `RetryMethods` 逐个列出幂等方法。
- `server/log/database/redis/grpc/registry` 是启动配置，运行时变更必须重启；动态配置用 `OnConfigChange` 校验、`OnConfigApplied` 应用。
- 对外错误不要返回内部 `err.Error()`；内部错误写日志，对外使用固定文案和 `resp.FailStatus`。

## 验证

提交前至少运行：

```bash
go test ./...
go test -race ./...
go vet ./...
govulncheck ./...
git diff --check
```
