# contrib 扩展

这里存放可选的第三方适配器。扩展使用独立 Go module，核心包不会因为业务不使用某个
后端而额外引入扩展自身的直接依赖。

`example/gateway` 只使用核心 HTTP/Gin 能力；`example/cmd` 展示带数据库和 Redis 的完整 service。

当前提供：

- `contrib/registry/etcd`：etcd 注册中心构造器（etcd v3.7.1）
- `contrib/registry/consul`：Consul 注册中心构造器（Consul API v1.34.4）
- `contrib/telemetry/gorm`：GORM OpenTelemetry instrumentation（GORM v1.31.2）
- `contrib/telemetry/redis`：go-redis OpenTelemetry hook（go-redis v9.22.0）
- `contrib/telemetry/otlphttp`：OTLP HTTP trace exporter（OTel v1.46.0）
- `contrib/migrate/postgres`：PostgreSQL migration library and CLI（golang-migrate v4.19.1）
- `contrib/stream/redis`：Redis Stream 发布、消费组、ACK 与死信封装（go-redis v9.22.0）
- `contrib/mid/ratelimit/redis`：Redis 分布式限流中间件（go-redis v9.22.0）
- `contrib/mid/jwt`：JWT 认证中间件（golang-jwt/jwt v5.3.1）

后续可按相同模式增加 Nacos、Kubernetes、Eureka 等 adapter。业务层只依赖核心
`registry` 接口，并显式引入所需扩展。

Telemetry 适配器不会由 `boot` 自动加载。服务在创建 `boot.App` 后按需接入：

```go
db.Use(gormotel.NewGORMPlugin(app.Telemetry))
app.Redis.AddHook(redisotel.NewRedisHook(app.Telemetry))
```
