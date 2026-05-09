package redis

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	Client *redis.Client
	Ctx    = context.Background()
)

// Init initializes the Redis client if REDIS_URL is set.
func Init() {
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		return
	}

	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		slog.Error("failed to parse REDIS_URL", "error", err)
		return
	}

	Client = redis.NewClient(opt)
	if err := Client.Ping(Ctx).Err(); err != nil {
		slog.Error("failed to connect to redis", "error", err)
		Client = nil
		return
	}

	slog.Info("redis connected", "url", redisURL)
}

// Close closes the Redis client connection.
func Close() error {
	if Client == nil {
		return nil
	}
	return Client.Close()
}

// IsEnabled returns true if Redis is configured and connected.
func IsEnabled() bool {
	return Client != nil
}

// IncrementRateLimit atomically increments a per-IP request counter with expiration.
func IncrementRateLimit(ip string, window time.Duration, maxRequests int) (allowed bool, resetAt time.Time, err error) {
	if Client == nil {
		return true, time.Now().Add(window), nil
	}

	key := "ratelimit:" + ip
	now := time.Now()

	pipe := Client.Pipeline()
	incr := pipe.Incr(Ctx, key)
	pipe.Expire(Ctx, key, window)
	_, err = pipe.Exec(Ctx)
	if err != nil {
		return false, now, err
	}

	count := incr.Val()
	if count > int64(maxRequests) {
		ttl := Client.TTL(Ctx, key).Val()
		return false, now.Add(ttl), nil
	}

	return true, now.Add(window), nil
}

// PublishWSMessage publishes a WebSocket broadcast message to Redis.
func PublishWSMessage(data []byte) error {
	if Client == nil {
		return nil
	}
	return Client.Publish(Ctx, "ws:broadcast", data).Err()
}

// SubscribeWSBroadcast subscribes to WebSocket broadcast messages from Redis.
func SubscribeWSBroadcast(handler func([]byte)) {
	if Client == nil {
		return
	}

	pubsub := Client.Subscribe(Ctx, "ws:broadcast")
	defer pubsub.Close()

	ch := pubsub.Channel()
	for msg := range ch {
		handler([]byte(msg.Payload))
	}
}

// SessionExists checks if a session token hash exists in Redis (fallback to DB handled by caller).
func SessionExists(tokenHash string) (bool, error) {
	if Client == nil {
		return false, nil
	}
	exists, err := Client.Exists(Ctx, "session:"+tokenHash).Result()
	return exists > 0, err
}

// SetSession stores a session in Redis with TTL.
func SetSession(tokenHash string, userID string, ttl time.Duration) error {
	if Client == nil {
		return nil
	}
	return Client.Set(Ctx, "session:"+tokenHash, userID, ttl).Err()
}

// DeleteSession removes a session from Redis.
func DeleteSession(tokenHash string) error {
	if Client == nil {
		return nil
	}
	return Client.Del(Ctx, "session:"+tokenHash).Err()
}

// DeleteUserSessions removes all Redis sessions for a user.
func DeleteUserSessions(userID string) error {
	if Client == nil {
		return nil
	}
	// Use a pattern to find and delete all sessions for this user
	// This is a simplified approach; in production, use a set or hash
	iter := Client.Scan(Ctx, 0, "session:*", 0).Iterator()
	for iter.Next(Ctx) {
		key := iter.Val()
		uid, err := Client.Get(Ctx, key).Result()
		if err == nil && uid == userID {
			Client.Del(Ctx, key)
		}
	}
	return iter.Err()
}

// CacheDeviceMetrics caches device metrics with TTL.
func CacheDeviceMetrics(deviceID string, data string, ttl time.Duration) error {
	if Client == nil {
		return nil
	}
	return Client.Set(Ctx, "metrics:"+deviceID, data, ttl).Err()
}

// GetCachedDeviceMetrics retrieves cached device metrics.
func GetCachedDeviceMetrics(deviceID string) (string, error) {
	if Client == nil {
		return "", nil
	}
	return Client.Get(Ctx, "metrics:"+deviceID).Result()
}
