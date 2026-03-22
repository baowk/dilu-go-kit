# dilu-go-kit — Claude Code 项目约定

## 本仓库结构

- `boot/` — 服务启动（Config/Logger/DB/Redis/gRPC/Registry）
- `resp/` — 统一 HTTP 响应（Ok/Fail/Page/Error）
- `mid/` — 中间件（JWT/CORS/Recovery/RateLimit）
- `store/` — 数据访问基础类型（ListOpts）
- `registry/` — 服务注册与发现（etcd）
- `example/` — 完整示例服务
- `docs/` — 开发规范 + 快速开始

## 开发约定

- 详细规范见 `docs/conventions.md`
- 快速上手见 `docs/quickstart.md`
