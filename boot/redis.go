package boot

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// OpenRedis creates a Redis client. Returns nil if Addr is empty.
func OpenRedis(cfg RedisConfig) (*redis.Client, error) {
	if cfg.Addr == "" {
		return nil, nil
	}
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Username: cfg.Username,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return rdb, nil
}
