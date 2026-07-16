# 快速开始

## 安装

```bash
go get github.com/baowk/dilu-go-kit@latest
```

## 5 分钟创建一个服务

### 1. 项目结构

```
my-service/
  cmd/main.go
  migrations/
  internal/xxx/
    model/        <- 手写结构体（gorm tag，一张表一个文件）
      task.go
      task_comment.go
    store/        <- Store 接口 + PG 实现（一张表一个 *_pg.go）
      store.go    <- 只放 Stores 聚合、Init、S
      task_pg.go
      task_comment_pg.go
    service/      <- 业务逻辑（一类资源一个文件）
      task.go
      task_comment.go
    service/dto/  <- 请求/响应 DTO（一类资源一个文件）
      task.go
      task_comment.go
    apis/         <- HTTP handler（一类资源一个文件）
      task_api.go
      task_comment_api.go
    router/       <- 路由注册
  resources/config.dev.yaml
  go.mod
```

### 2. 入口文件

```go
// cmd/main.go
package main

import (
    "log"
    "github.com/baowk/dilu-go-kit/boot"
    "github.com/baowk/dilu-go-kit/mid"
    "my-service/internal/xxx/router"
    "my-service/internal/xxx/store"
)

func main() {
    app, err := boot.New("resources/config.dev.yaml")
    if err != nil {
        log.Fatal(err)
    }
    app.Run(func(a *boot.App) error {
        store.Init(a.DB("main"))
        mid.Default(a.Gin, mid.DefaultConfig{
            CORS:        mid.CORSCfg{Enable: true, Mode: "allow-all"},
            AccessLimit: mid.AccessLimitCfg{
                Enable: true,
                Total: 300,
                Duration: 5,
                Backend: "redis",
                Redis: a.Redis,
                KeyPrefix: "my-service:ratelimit",
            },
        })
        cfg := a.GetConfig()
        router.Init(a.Gin, mid.JWTConfig{
            Secret: cfg.JWT.Secret, Issuer: cfg.JWT.Issuer,
            Audience: cfg.JWT.Audience,
        })
        return nil
    })
}
```

### 3. 配置文件

```yaml
# resources/config.dev.yaml
server:
  name: my-service
  addr: ":8080"                  # 监听地址
  # advertiseAddr: "10.0.1.5:8080" # 注册发现地址；空时从 addr 推断
  mode: debug

# log:                        # 默认 console 输出，可选 file / both
#   output: both
#   file:
#     path: "logs/app.log"
#     maxAge: 7

database:
  main:
    dsn: ""                  # 建议用 DATABASE_MAIN_DSN / DATABASE_DSN 注入
    maxIdle: 10
    maxOpen: 50
    maxLifetime: 3600
    maxIdleTime: 300
    slowThreshold: 200    # ms, 超过记录慢查询日志

redis:
  addr: "127.0.0.1:6379"
  password: ""               # 建议用 REDIS_PASSWORD 注入

grpc:
  enable: false

registry:
  enable: true
  type: etcd                # etcd / consul
  endpoints:
    - "127.0.0.1:2379"
  # configKey: "/config/"   # 启用远程配置（自动拼 server.name）

jwt:
  secret: ""                # 使用 JWT_SECRET 注入
  issuer: auth-service
  audience: ["my-service"]
```

### 3.1 数据库迁移

```bash
go run github.com/baowk/dilu-go-kit/cmd/migrate -dir migrations create -name init
DATABASE_DSN='postgres://user:pass@127.0.0.1:5432/my_service?sslmode=disable' \
  go run github.com/baowk/dilu-go-kit/cmd/migrate -dir migrations up
```

`create` 生成严格递增的 UnixNano 数字版本。已有迁移历史不要切换到位数更短、数值更小
的日期格式。多库服务只设置 `DATABASE_<NAME>_DSN`，不要使用会覆盖所有库的
`DATABASE_DSN`。

### 4. Model

```go
// internal/xxx/model/task.go
package model

import "time"

type Task struct {
    ID          int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
    WorkspaceID int64     `gorm:"column:workspace_id;index" json:"workspace_id"`
    Title       string    `gorm:"column:title;size:200" json:"title"`
    Status      int16     `gorm:"column:status;default:1" json:"status"`
    CreatedAt   time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
    UpdatedAt   time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (Task) TableName() string { return "task" }
```

新增表时不要追加到 `task.go` 或集中到 `model.go`；例如新增 `task_comment` 表，应创建 `model/task_comment.go`。

### 5. Store

```go
// internal/xxx/store/store.go
package store

import (
    "context"
    "my-service/internal/xxx/model"
    base "github.com/baowk/dilu-go-kit/store"
    "gorm.io/gorm"
)

type TaskStore interface {
    GetByID(ctx context.Context, workspaceID, id int64) (*model.Task, error)
    List(ctx context.Context, workspaceID int64, opts base.ListOpts) ([]*model.Task, int64, error)
    Create(ctx context.Context, t *model.Task) error
    Update(ctx context.Context, workspaceID, id int64, updates map[string]any) (int64, error)
    Delete(ctx context.Context, workspaceID, id int64) (int64, error)
}

type Stores struct{ Task TaskStore }

var s *Stores

func Init(db *gorm.DB) { s = &Stores{Task: &pgTaskStore{db: db}} }
func S() *Stores       { return s }
```

`store/store.go` 只放聚合和初始化；具体 SQL 实现按表放到 `store/{table}_pg.go`，不要集中写进 `store.go` 或 `store_pg.go`。

```go
// internal/xxx/store/task_pg.go
package store

import (
    "context"
    "my-service/internal/xxx/model"
    base "github.com/baowk/dilu-go-kit/store"
    "gorm.io/gorm"
)

type pgTaskStore struct{ db *gorm.DB }

func (s *pgTaskStore) GetByID(ctx context.Context, workspaceID, id int64) (*model.Task, error) {
    var t model.Task
    err := s.db.WithContext(ctx).Where("workspace_id = ? AND id = ?", workspaceID, id).First(&t).Error
    return &t, err
}

func (s *pgTaskStore) List(ctx context.Context, workspaceID int64, opts base.ListOpts) ([]*model.Task, int64, error) {
    var total int64
    q := s.db.WithContext(ctx).Model(&model.Task{}).Where("workspace_id = ?", workspaceID)
    if err := q.Count(&total).Error; err != nil {
        return nil, 0, err
    }
    var list []*model.Task
    err := q.Order("id DESC").Offset(opts.Offset()).Limit(opts.PageSize()).Find(&list).Error
    return list, total, err
}

func (s *pgTaskStore) Create(ctx context.Context, t *model.Task) error {
    return s.db.WithContext(ctx).Create(t).Error
}

func (s *pgTaskStore) Update(ctx context.Context, workspaceID, id int64, updates map[string]any) (int64, error) {
    r := s.db.WithContext(ctx).Model(&model.Task{}).Where("workspace_id = ? AND id = ?", workspaceID, id).Updates(updates)
    return r.RowsAffected, r.Error
}

func (s *pgTaskStore) Delete(ctx context.Context, workspaceID, id int64) (int64, error) {
    r := s.db.WithContext(ctx).Where("workspace_id = ? AND id = ?", workspaceID, id).Delete(&model.Task{})
    return r.RowsAffected, r.Error
}
```

### 6. API Handler

```go
// internal/xxx/apis/task_api.go
package apis

import (
    "net/http"
    "strconv"
    "github.com/gin-gonic/gin"
    "github.com/baowk/dilu-go-kit/mid"
    "github.com/baowk/dilu-go-kit/resp"
    base "github.com/baowk/dilu-go-kit/store"
    "my-service/internal/xxx/store"
)

type TaskAPI struct{}

func (a *TaskAPI) List(c *gin.Context) {
    page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
    size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
    workspaceID := mid.GetWorkspaceID(c)
    if workspaceID <= 0 {
        resp.FailStatus(c, http.StatusForbidden, resp.CodeForbidden, "缺少工作区权限")
        return
    }
    list, total, err := store.S().Task.List(c, workspaceID, base.ListOpts{Page: page, Size: size})
    if err != nil {
        resp.FailStatus(c, http.StatusInternalServerError, resp.CodeDBError, "数据库错误")
        return
    }
    resp.Page(c, list, total, page, size)
}
```

### 7. Router

```go
// internal/xxx/router/router.go
package router

import (
    "github.com/gin-gonic/gin"
    "github.com/baowk/dilu-go-kit/mid"
    "my-service/internal/xxx/apis"
)

func Init(r *gin.Engine, jwtConfig mid.JWTConfig) {
    api := &apis.TaskAPI{}
    // JWT secret 从 boot.Config 读取，在 main.go 传入或从配置获取
    // JWT 必须包含 exp 和 workspace_id（或 wid）claim
    auth := r.Group("/v1/tasks").Use(mid.JWT(jwtConfig))
    // Handler 中可通过 mid.GetUID/GetTenantID/GetShopIDs/GetScopes 获取身份上下文
    {
        auth.GET("", api.List)
    }
}
```

### 8. 运行

```bash
go run cmd/main.go
# => HTTP server started on :8080
# => registry: registered service=my-service addr=10.0.1.5:8080
```

多节点运行可用环境变量区分：

```bash
REMOTE_NODE=node-1 SERVER_ADDR=:7801 SERVER_ADVERTISE_ADDR=10.0.1.5:7801 DATABASE_MAIN_DSN='...' JWT_SECRET='...' ./my-service
REMOTE_NODE=node-2 SERVER_ADDR=:7802 SERVER_ADVERTISE_ADDR=10.0.1.6:7802 DATABASE_MAIN_DSN='...' JWT_SECRET='...' ./my-service
```

## 完整示例

参见 [example/](../example/) 目录。
