# 快速开始

## 安装

```bash
go get github.com/mofang-ai/mofang-go-kit@latest
```

## 5 分钟创建一个服务

### 1. 项目结构

```
my-service/
  cmd/main.go
  internal/modules/xxx/
    model/        ← 手写结构体（gorm tag）
    store/        ← Store 接口 + PG 实现
    service/      ← 业务逻辑
    apis/         ← HTTP handler
    router/       ← 路由注册
  resources/config.dev.yaml
  go.mod
```

### 2. 入口文件

```go
// cmd/main.go
package main

import (
    "log"
    "github.com/mofang-ai/mofang-go-kit/boot"
    "github.com/mofang-ai/mofang-go-kit/mid"
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
        a.Gin.Use(mid.Recovery(), mid.CORS())
        router.Init(a.Gin, "jwt-secret")
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

redis:
  addr: "127.0.0.1:6379"

grpc:
  enable: false
```

### 4. Model

```go
// internal/modules/xxx/model/task.go
type Task struct {
    ID        int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
    Title     string    `gorm:"column:title;size:200" json:"title"`
    CreatedAt time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
}
func (Task) TableName() string { return "task" }
```

### 5. Store

```go
// internal/modules/xxx/store/store.go
type TaskStore interface {
    GetByID(ctx context.Context, id int64) (*model.Task, error)
    List(ctx context.Context, opts base.ListOpts) ([]*model.Task, int64, error)
    Create(ctx context.Context, t *model.Task) error
}

// internal/modules/xxx/store/task_pg.go
type pgTaskStore struct{ db *gorm.DB }
// ... 实现接口方法

// internal/modules/xxx/store/init.go
func Init(db *gorm.DB) { s = &Stores{Task: &pgTaskStore{db: db}} }
func S() *Stores { return s }
```

### 6. API Handler

```go
// internal/modules/xxx/apis/task_api.go
func (a *TaskAPI) List(c *gin.Context) {
    list, total, err := store.S().Task.List(c, base.ListOpts{Page: 1, Size: 20})
    if err != nil {
        resp.Fail(c, 50001, err.Error())
        return
    }
    resp.Page(c, list, total, 1, 20)
}
```

### 7. 运行

```bash
go run cmd/main.go
# => HTTP server started on :8080
```

## 完整示例

参见 [example/](../example/) 目录。
