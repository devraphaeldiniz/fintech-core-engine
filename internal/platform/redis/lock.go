package redis

import (
	"context"
	"math/rand"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const releaseLockLua = `
if redis.call("get", KEYS[1]) == ARGV[1] then
    return redis.call("del", KEYS[1])
else
    return 0
end
`

type DistributedLock struct {
	client *redis.Client
	key    string
	token  string
	ttl    time.Duration
}

func NewLock(client *redis.Client, key string, ttl time.Duration) *DistributedLock {
	return &DistributedLock{
		client: client,
		key:    "lock:" + key,
		token:  uuid.NewString(),
		ttl:    ttl,
	}
}

// AcquireWithRetry tenta adquirir o lock repetidamente até o timeout estourar
func (l *DistributedLock) AcquireWithRetry(ctx context.Context, maxWait time.Duration) (bool, error) {
	deadline := time.Now().Add(maxWait)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		default:
		}

		ok, err := l.client.SetNX(ctx, l.key, l.token, l.ttl).Result()
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}

		// Jitter aleatório entre 10ms e 25ms para evitar thundering herd
		sleepDuration := time.Duration(10+rand.Intn(15)) * time.Millisecond
		time.Sleep(sleepDuration)
	}

	return false, nil
}

func (l *DistributedLock) Release(ctx context.Context) error {
	return l.client.Eval(ctx, releaseLockLua, []string{l.key}, l.token).Err()
}
