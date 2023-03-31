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
	return newLock(c.client, key, value, expiration), nil
}

type Lock struct {
	client     redis.Cmdable
	Key        string
	Value      string
	expiration time.Duration
	unlock     chan struct{}
}

func newLock(client redis.Cmdable, key string, value string, expiration time.Duration) *Lock {
	return &Lock{
		client:     client,
		Key:        key,
		Value:      value,
		expiration: expiration,
		unlock:     make(chan struct{}, 1),
	}
}
func (l *Lock) AutoRefresh(interval time.Duration, timeout time.Duration) error {
	ch := make(chan struct{}, 1)
	defer close(ch)
	ticker := time.NewTicker(interval)
	for {
		select {
		case <-ch:
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			err := l.Refresh(ctx)
			cancel()
			if err == context.DeadlineExceeded {
				//立即重试
				ch <- struct{}{}
				continue
			}
			if err != nil { //始终需要处理
				return err
			}
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			err := l.Refresh(ctx)
			cancel()
			if err == context.DeadlineExceeded {
				//立即重试
				ch <- struct{}{}
				continue
			}
			if err != nil { //始终需要处理
				return err
			}
		case <-l.unlock:
			return nil

		}
	}
}
func (l *Lock) Refresh(ctx context.Context) error {
	res, err := l.client.Eval(ctx, luaRefresh, []string{l.Key}, l.Value, l.expiration.Milliseconds()).Int64()
	if redis.Nil == err {
		return ErrLockNotHold
	}
	if err != nil {
		return err
	}
	//判断res是否是1
	if res == 1 {
		//这个锁不存在或者不是你的
		return ErrLockNotHold
	}
	return nil
}

var (
	//go:embed unlock.lua
	luaUnlock string
	//go:embed refresh.lua
	luaRefresh string
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

	res, err := l.client.Eval(ctx, luaUnlock, []string{l.Key}, l.Value).Int64()
	defer func() {
		l.unlock <- struct{}{}
		close(l.unlock)
	}()
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
