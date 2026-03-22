# mofang-go-kit

Go 微服务基础工具包。提供统一的服务启动、HTTP 响应、中间件和数据访问层规范。

## 特性

- **boot** — 一行代码启动服务（Config + Logger + DB + Redis + 优雅关闭 + gRPC）
- **resp** — 统一 JSON 响应格式（Ok / Fail / Page / Error）
- **mid** — 开箱即用的中间件（JWT / CORS / Recovery / RateLimit）
- **store** — 数据访问层基础类型（ListOpts 分页）

## 安装

```bash
go get github.com/mofang-ai/mofang-go-kit@latest
```

## 快速开始

```go
package main

import (
    "log"
    "github.com/mofang-ai/mofang-go-kit/boot"
    "github.com/mofang-ai/mofang-go-kit/mid"
    "github.com/mofang-ai/mofang-go-kit/resp"
)

func main() {
    app, _ := boot.New("config.yaml")
    app.Run(func(a *boot.App) error {
        a.Gin.Use(mid.Recovery(), mid.CORS())
        a.Gin.GET("/ping", func(c *gin.Context) {
            resp.Ok(c, "pong")
        })
        return nil
    })
}
```

详见 [docs/quickstart.md](docs/quickstart.md) 和 [example/](example/)。

## 目录

```
boot/       服务启动（Config/Logger/DB/Redis/App 生命周期）
resp/       统一 HTTP 响应
mid/        中间件（JWT/CORS/Recovery/RateLimit）
store/      基础类型（ListOpts）
example/    完整示例服务
docs/       规范文档
```

## 规范

- [开发规范](docs/conventions.md) — 项目结构、数据层、API、错误码
- [快速开始](docs/quickstart.md) — 5 分钟上手

## License

MIT
