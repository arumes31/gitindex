// Package cache is a thin, failure-tolerant wrapper around Redis. If Redis is
// unavailable the app keeps working (degraded: it just refetches from GitHub),
// so cache errors are logged, never fatal.
package cache

import (
	"context"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

type Cache struct {
	rdb *redis.Client
}

func New(addr, password string, db int) *Cache {
	rdb := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           db,
		DialTimeout:  3 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	})
	return &Cache{rdb: rdb}
}

func (c *Cache) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

// GetBytes returns (value, true) on a hit, (nil, false) on miss or any error.
func (c *Cache) GetBytes(ctx context.Context, key string) ([]byte, bool) {
	b, err := c.rdb.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, false
	}
	if err != nil {
		log.Printf("[cache] get %q failed: %v", key, err)
		return nil, false
	}
	return b, true
}

func (c *Cache) SetBytes(ctx context.Context, key string, val []byte, ttl time.Duration) {
	if err := c.rdb.Set(ctx, key, val, ttl).Err(); err != nil {
		log.Printf("[cache] set %q failed: %v", key, err)
	}
}

func (c *Cache) SetNX(ctx context.Context, key, val string, ttl time.Duration) (bool, error) {
	return c.rdb.SetNX(ctx, key, val, ttl).Result()
}

func (c *Cache) Del(ctx context.Context, key string) {
	if err := c.rdb.Del(ctx, key).Err(); err != nil {
		log.Printf("[cache] del %q failed: %v", key, err)
	}
}

func (c *Cache) Close() error { return c.rdb.Close() }
