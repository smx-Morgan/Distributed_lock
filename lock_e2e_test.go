package redisLock

import (
	"context"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"lock/Lock"
	"testing"
	"time"
)

type ClientE2ESuite struct {
	suite.Suite
	rdb redis.Cmdable
}

func (s *ClientE2ESuite) SetupSuite() {
	s.rdb = redis.NewClient(&redis.Options{
		Addr:     "127.0.0.1:6379",
		Password: "roots",
		DB:       7, // 关注列表信息存入 DB0.
	})
	// 确保测试的目标 Redis 已经启动成功了
	for s.rdb.Ping(context.Background()).Err() != nil {

	}
}

func TestLock(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     "120.25.2.146:6379",
		Password: "roots",
		DB:       5, // 关注列表信息存入 DB0.
	})
	testCases := []struct {
		//测试场景
		name string
		//准备数据
		before func()
		//校验数据
		after func()
		//输入
		key        string
		expiration time.Duration
		//设置mock数据
		mock func() redis.Cmdable
		//预期输出 测试方法返回值
		wantErr  error
		wantLock *Lock.Lock
	}{
		{
			name: "locked",
			before: func() {

			},
			after: func() {
				res, err := rdb.Del(context.Background(), "locked-key").Result()
				require.NoError(t, err)
				require.Equal(t, int64(1), res)
			},
			key:        "locked-key",
			expiration: time.Minute,
			wantLock: &Lock.Lock{
				Key: "locked-key",
			},
		}, {
			name: "failed to lock",
			key:  "failed-key",
			before: func() {
				val, err := rdb.Set(context.Background(), "failed-key", "123", time.Minute).Result()
				require.NoError(t, err)
				require.Equal(t, "OK", val)
			},
			after: func() {
				res, err := rdb.Get(context.Background(), "failed-key").Result()
				require.NoError(t, err)
				require.Equal(t, "123", res)
			},
			expiration: time.Minute,
			wantErr:    Lock.ErrFailedTOPreemptLock,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.before()
			c := Lock.NewClient(rdb)
			l, err := c.TryLock(context.Background(), tc.key, tc.expiration)
			tc.after()
			assert.Equal(t, tc.wantErr, err)
			if err != nil {
				return
			}
			assert.Equal(t, tc.wantLock.Key, l.Key)
			assert.NotEmpty(t, l.Value)
		})
	}
}
