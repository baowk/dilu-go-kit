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
  internal/modules/xxx/
    model/        <- 手写结构体（gorm tag）
    store/        <- Store 接口 + PG 实现
    service/      <- 业务逻辑
    apis/         <- HTTP handler
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
    "my-service/internal/modules/xxx/router"
    "my-service/internal/modules/xxx/store"
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
            AccessLimit: mid.AccessLimitCfg{Enable: true, Total: 300, Duration: 5},
        })
        router.Init(a.Gin)
        return nil
    })
}
```

### 3. 配置文件

```yaml
# resources/config.dev.yaml
server:
  name: my-service
  addr: ":8080"
  mode: debug

database:
  main:
    dsn: "host=127.0.0.1 user=postgres dbname=mydb sslmode=disable"
    maxIdle: 10
    maxOpen: 50
    maxLifetime: 3600
    maxIdleTime: 300
    slowThreshold: 200    # ms, 超过记录慢查询日志

redis:
  addr: "127.0.0.1:6379"

grpc:
  enable: false

registry:
  enable: true
  endpoints:
    - "127.0.0.1:2379"
```

### 4. Model

```go
// internal/modules/xxx/model/task.go
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

### 5. Store

```go
// internal/modules/xxx/store/store.go
package store

import (
    "context"
    "my-service/internal/modules/xxx/model"
    base "github.com/baowk/dilu-go-kit/store"
    "gorm.io/gorm"
)

type TaskStore interface {
    GetByID(ctx context.Context, id int64) (*model.Task, error)
    List(ctx context.Context, opts base.ListOpts) ([]*model.Task, int64, error)
    Create(ctx context.Context, t *model.Task) error
    Update(ctx context.Context, id int64, updates map[string]any) (int64, error)
    Delete(ctx context.Context, id int64) (int64, error)
}

type Stores struct{ Task TaskStore }

var s *Stores

func Init(db *gorm.DB) { s = &Stores{Task: &pgTaskStore{db: db}} }
func S() *Stores       { return s }
```

```go
// internal/modules/xxx/store/task_pg.go
package store

import (
    "context"
    "my-service/internal/modules/xxx/model"
    base "github.com/baowk/dilu-go-kit/store"
    "gorm.io/gorm"
)

type pgTaskStore struct{ db *gorm.DB }

func (s *pgTaskStore) GetByID(ctx context.Context, id int64) (*model.Task, error) {
    var t model.Task
    err := s.db.WithContext(ctx).Where("id = ?", id).First(&t).Error
    return &t, err
}

func (s *pgTaskStore) List(ctx context.Context, opts base.ListOpts) ([]*model.Task, int64, error) {
    var total int64
    q := s.db.WithContext(ctx).Model(&model.Task{})
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

func (s *pgTaskStore) Update(ctx context.Context, id int64, updates map[string]any) (int64, error) {
    r := s.db.WithContext(ctx).Model(&model.Task{}).Where("id = ?", id).Updates(updates)
    return r.RowsAffected, r.Error
}

func (s *pgTaskStore) Delete(ctx context.Context, id int64) (int64, error) {
    r := s.db.WithContext(ctx).Where("id = ?", id).Delete(&model.Task{})
    return r.RowsAffected, r.Error
}
```

### 6. API Handler

```go
// internal/modules/xxx/apis/task_api.go
package apis

import (
    "strconv"
    "github.com/gin-gonic/gin"
    "github.com/baowk/dilu-go-kit/mid"
    "github.com/baowk/dilu-go-kit/resp"
    base "github.com/baowk/dilu-go-kit/store"
    "my-service/internal/modules/xxx/store"
)

type TaskAPI struct{}

func (a *TaskAPI) List(c *gin.Context) {
    page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
    size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
    list, total, err := store.S().Task.List(c, base.ListOpts{Page: page, Size: size})
    if err != nil {
        resp.Fail(c, resp.CodeDBError, err.Error())
        return
    }
    resp.Page(c, list, total, page, size)
}
```

### 7. Router

```go
// internal/modules/xxx/router/router.go
package router

import (
    "github.com/gin-gonic/gin"
    "github.com/baowk/dilu-go-kit/mid"
    "my-service/internal/modules/xxx/apis"
)

func Init(r *gin.Engine) {
    api := &apis.TaskAPI{}
    // JWT secret 从 boot.Config 读取，在 main.go 传入或从配置获取
    auth := r.Group("/v1/tasks").Use(mid.JWT(mid.JWTConfig{Secret: "your-secret", HeaderUID: "a_uid"}))
    {
        auth.GET("", api.List)
    }
}
```

### 8. 运行

```bash
go run cmd/main.go
# => HTTP server started on :8080
# => registry: registered service=my-service addr=:8080
```

## 完整示例

参见 [example/](../example/) 目录。
