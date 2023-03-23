package Lock

import (
	"context"
	_ "embed"
	"errors"
	"github.com/google/uuid"
	redis "github.com/redis/go-redis/v9"
	"time"
)

type Client struct {
	client redis.Cmdable
	//redis.ClusterClient 集群
	//redis.Client 单机
}

var (
	ErrLockNotHold         = errors.New("未持有锁")
	ErrFailedTOPreemptLock = errors.New("加锁失败")
)

func NewClient(client redis.Cmdable) *Client {
	return &Client{client: client}
}
func (c *Client) TryLock(ctx context.Context, key string, expiration time.Duration) (*Lock, error) {
	value := uuid.New().String()
	res, err := c.client.SetNX(ctx, key, value, expiration).Result()
	if err != nil {
		return nil, err
	}
	if !res {
		return nil, ErrFailedTOPreemptLock
	}
	return newLock(c.client, key, value), nil
}

type Lock struct {
	client redis.Cmdable
	key    string
	value  string
}

func newLock(client redis.Cmdable, key string, value string) *Lock {
	return &Lock{
		client: client,
		key:    key,
		value:  value,
	}
}

var (
	//go:embed unlock.lua
	luaUnlock string
)

func (l *Lock) UnLock(ctx context.Context) error {
	//确保是这把锁
	/*



		val, err := l.client.Get(ctx,l.key).Result()
		if err != nil {
			return err
		}
		if val == l.value{
			_, err := l.client.Del(ctx, l.key).Result()
			if err != nil {
				return err
			}
		}

		//if res != 1 {
		//过期，被删
		//	return errors.New("解锁失败")
		//}
		return nil

	*/

	res, err := l.client.Eval(ctx, luaUnlock, []string{l.key}, l.value).Int64()
	if redis.Nil == err {
		return ErrLockNotHold
	}
	if err != nil {
		return err
	}
	//判断res是否是1
	if res == 0 {
		//这个锁不存在或者不是你的
		return ErrLockNotHold
	}
	return nil
}
