# 开发规范

## 一、项目结构

```
services/my-service/
  cmd/main.go                       ← 入口
  internal/modules/{module}/
    model/                          ← 手写结构体（一张表一个文件）
    store/                          ← Store 接口 + PG 实现 + Init/S()
    service/                        ← 业务逻辑
      dto/                          ← 请求/响应 DTO
    apis/                           ← HTTP handler（调用 resp 包）
    grpc/                           ← gRPC handler（可选）
    router/                         ← 路由注册
  internal/common/                  ← 服务内公共代码
    config/                         ← 自定义配置扩展
    middleware/                     ← 服务特有中间件
  resources/config.dev.yaml         ← 配置文件
  go.mod
```

## 二、数据访问层

### Model

```go
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

### 响应格式

```json
// 成功
{"code": 200, "msg": "OK", "data": {...}}

// 失败
{"code": 40001, "msg": "参数错误"}

// 分页
{"code": 200, "data": {"list": [], "total": 100, "pageSize": 20, "currentPage": 1}}
```

### 错误码

```
200       成功
400xx     参数错误
401xx     认证错误（未登录/Token 过期/无效）
403xx     权限错误
404xx     资源不存在
409xx     冲突
429xx     限流
500xx     服务端错误
```

### 分页参数

```
page — 页码，从 1 开始，默认 1
size — 每页条数，默认 20，最大 500
```

## 四、中间件使用

```go
// Recovery + CORS（全局）
r.Use(mid.Recovery(), mid.CORS())

// JWT（需认证的路由组）
auth := r.Group("/v1/xxx").Use(mid.JWT(mid.JWTConfig{Secret: "..."}))

// 限流（可选）
r.Use(mid.RateLimit(100, time.Minute))

// 获取当前用户
uid := mid.GetUID(c)
```

## 五、配置

```yaml
server:
  name: my-service
  addr: ":8080"
  mode: debug        # debug / release

database:
  main:
    dsn: "host=127.0.0.1 user=postgres dbname=mydb sslmode=disable"
    maxIdle: 10
    maxOpen: 50
    maxLifetime: 3600  # seconds

redis:
  addr: "127.0.0.1:6379"
  password: ""
  db: 0

grpc:
  enable: false
  addr: ":9090"
```

扩展配置：嵌入 `boot.Config` 或用 `boot.LoadConfig(path, &myConfig)` 自定义。
