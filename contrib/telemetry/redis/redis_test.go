package redis

import (
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestRedisHookImplementsHook(t *testing.T) {
	var _ redis.Hook = (*RedisHook)(nil)
}
