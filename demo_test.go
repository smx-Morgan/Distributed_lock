package redisLock

import (
	"context"
	"errors"
	"github.com/golang/mock/gomock"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"lock/Lock"
	"lock/mocks"
	"testing"
	"time"
)

func TestName(t *testing.T) {

}

// 单元测试
func TestClient_TryLock(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	testCases := []struct {
		//测试场景
		name string
		//输入
		key        string
		expiration time.Duration
		//设置mock数据
		mock func() redis.Cmdable
		//预期输出 测试方法返回值
		wantErr  error
		wantLock *Lock.Lock
	}{
		//成功案例
		{
			name:       "locked",
			key:        "locked-key",
			expiration: time.Minute,
			mock: func() redis.Cmdable {
				rdb := mocks.NewMockCmdable(ctrl)
				res := redis.NewBoolResult(true, nil)
				rdb.EXPECT().SetNX(gomock.Any(), "locked-key", gomock.Any(), time.Minute).
					Return(res)
				return rdb
			},
			wantLock: &Lock.Lock{
				Key: "locked-key",
			},
		},
		//网络错误
		{
			name:       "network",
			key:        "network-key",
			expiration: time.Minute,
			mock: func() redis.Cmdable {
				rdb := mocks.NewMockCmdable(ctrl)
				res := redis.NewBoolResult(false, errors.New("network error"))
				rdb.EXPECT().SetNX(gomock.Any(), "network-key", gomock.Any(), time.Minute).
					Return(res)
				return rdb
			},
			wantErr: errors.New("network error"),
		},
		//redis被手动删除
		{
			name:       "failed to lock",
			key:        "failed-key",
			expiration: time.Minute,
			mock: func() redis.Cmdable {
				rdb := mocks.NewMockCmdable(ctrl)
				res := redis.NewBoolResult(false, nil)
				rdb.EXPECT().
					SetNX(gomock.Any(), "failed-key", gomock.Any(), time.Minute).
					Return(res)
				return rdb
			},
			wantErr: Lock.ErrFailedTOPreemptLock,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			c := Lock.NewClient(tc.mock())
			l, err := c.TryLock(context.Background(), tc.key, tc.expiration)
			assert.Equal(t, tc.wantErr, err)
			if err != nil {
				return
			}
			assert.Equal(t, tc.wantLock.Key, l.Key)
			assert.NotEmpty(t, l.Value)
		})
	}
}
