# dilu-go-kit

Go 微服务基础工具包。提供统一的服务启动、HTTP 响应、中间件、数据访问层规范和服务注册发现。

## 特性

- **boot** — 一行代码启动服务（Config + Logger + DB + Redis + gRPC + 优雅关闭）
- **resp** — 统一 JSON 响应格式（Ok / Fail / Page / Error）
- **mid** — 开箱即用的中间件（JWT / CORS / Recovery / RateLimit）
- **store** — 数据访问层基础类型（ListOpts 分页）
- **registry** — 服务注册与发现（etcd，启动自动注册，关闭自动注销）

## 安装

```bash
go get github.com/baowk/dilu-go-kit@latest
```

## 快速开始

```go
package main

import (
    "log"
    "github.com/baowk/dilu-go-kit/boot"
    "github.com/baowk/dilu-go-kit/mid"
    "github.com/baowk/dilu-go-kit/resp"
    "github.com/gin-gonic/gin"
)

func main() {
    app, err := boot.New("config.yaml")
    if err != nil {
        log.Fatal(err)
    }
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
boot/       服务启动（Config/Logger/DB/Redis/App 生命周期/gRPC/Registry）
resp/       统一 HTTP 响应
mid/        中间件（JWT/CORS/Recovery/RateLimit）
store/      基础类型（ListOpts）
registry/   服务注册与发现（etcd）
example/    完整示例服务
docs/       规范文档
```

## 服务注册与发现

```yaml
# config.yaml
registry:
  enable: true
  endpoints:
    - "127.0.0.1:2379"
```

服务启动时自动注册到 etcd，关闭时自动注销。网关可 Watch 实时发现后端服务变更。

## AI 辅助开发（Claude Code）

本仓库提供 [CLAUDE.template.md](CLAUDE.template.md)，新项目拷贝到根目录并重命名为 `CLAUDE.md`，Claude Code 打开项目时会自动读取，遵循 dilu-go-kit 的开发规范（数据层、API 格式、中间件用法等）。

```bash
# 新项目初始化时
cp $(go env GOMODCACHE)/github.com/baowk/dilu-go-kit@*/CLAUDE.template.md ./CLAUDE.md
# 或直接从 GitHub 下载
curl -sL https://raw.githubusercontent.com/baowk/dilu-go-kit/main/CLAUDE.template.md > CLAUDE.md
```

拷贝后按需修改项目名和结构说明即可。

## 规范

- [开发规范](docs/conventions.md) — 项目结构、数据层、API、错误码
- [快速开始](docs/quickstart.md) — 5 分钟上手

## License

MIT
