package boot

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// OpenRedis creates a Redis client. Returns nil if Addr is empty.
func OpenRedis(cfg RedisConfig) (*redis.Client, error) {
	if cfg.Addr == "" {
		return nil, nil
	}
	options := &redis.Options{
		Addr:     cfg.Addr,
		Username: cfg.Username,
		Password: cfg.Password,
		DB:       cfg.DB,
	}
	if cfg.TLS.Enable {
		tlsConfig, err := loadTLSConfig(cfg.TLS)
		if err != nil {
			return nil, fmt.Errorf("redis TLS: %w", err)
		}
		options.TLSConfig = tlsConfig
	}
	rdb := redis.NewClient(options)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return rdb, nil
}
