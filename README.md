# Distributed_lock

Using redis to impl a distributed lock

This distributed lock is mainly used to provide learning records for oneself and provide reference for other students to implement distributed locks. Therefore, there is no English version available. If you need an English version, please translate it yourself

分布式锁，简单来说就是在分布式环境下不同实例之间抢一把锁。

和普通的锁比起来，也就是抢锁的从线程（协程）变成了实例。

### 1.基础的锁

实现分布式锁的起点，就是利用setnx命令，确保可以排他地设置一个键值

```go
func (c *Client) TryLock(ctx context.Context, key string, expiration time.Duration) (*Lock, error) {
	value := uuid.New().String()
    //设置过期时间
	res, err := c.client.SetNX(ctx, key, value, expiration).Result()
	if err != nil {
		return nil, err
	}
	if !res {
		return nil, ErrFailedTOPreemptLock
	}
	return newLock(c.client, key, value, expiration), nil
}
```

Question 1 为什么需要过期时间

因为如果没有过期时间，那如果被加锁的实例崩溃，将没有人释放锁，会占用redis的内存

Question 2 为什么要用uuid作为值

我们需要一个唯一的值来比较这是某个实列的锁，只要能确保同一把锁的值不会冲突

### 2.锁的释放

释放锁需要做两件事

1. 看看是否是自己加的锁

2. 如果是直接释放

如果是自己的锁过期了，然后别人加了锁的情况，就不是自己的锁，或者是被后台更改

```go
func (l *Lock) UnLock(ctx context.Context) error {
	res, err := l.client.Eval(ctx, luaUnlock, []string{l.Key}, l.Value).Int64()
    //关闭锁
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
```

为了保证分布式事务的一致性，我们需要确保查看锁和释放锁同时进行，所以我们就要用到lua脚本

```lua
if redis.call("get", KEYS[1]) == ARGV[1]
then
    -- 刷新过期时间
    return redis.call("pexpire", KEYS[1], ARGV[2])
else
    -- 设置 key value 和过期时间
    return redis.call("set", KEYS[1], ARGV[1], "NX", "PX", ARGV[2])
end
```

Lua 脚本逻辑很简单：

•使用 redis get 命令查看 key 对应的值，如果相等，那么意味着自己的锁还在

•否则，说明锁已经被释放了，可能是过期了，也能是被人误删除了

### 3. 锁的过期与自动续约

我们很难确定锁的过期时间，设置短了，业务还没完成，锁就过期了

设置长了，万一实例崩溃就会长时间拿不到锁

所以我这里考虑使用续约的办法

过期时间设置短一点，如果锁还在就自动续约

如果实例崩溃，则自然过期

```go
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
```

•第一个地方是 redis.Nil 的检测，说明根本没有拿到锁

•第二个地方是意味着可能服务器出错了，或者超时了

•第三个地方意味着锁确实存在，但是却不是自己的锁

lua 脚本

```lua
if redis.call("get",KEYS[1]) == ARGV[1] then
    return redis.call("pexpire",KEYS[1],ARGV[2])
else
    return 0
end
```

Refresh 方法在使用的时候有三个问题：

•第一个问题：间隔多久续约一次？

答：这里我们让用户来指定多久续约一次，因为这个跟网络、Redis 服务器的稳定性有关，而每次续多长，我们就直接使用原本的过期时间。

•第二个问题：如果 Refresh 返回了 error，怎么处理？

•如果返回的是超时 error，要怎么办

答：我们选择再次尝试续约。超时意味着也不知道究竟有没有续约成功，而且大多数时候超时都是偶发性的，所以可以立刻再次尝试。缺点就是如果此时真的 Redis 服务器崩溃或者网络不通，那么会导致无限次尝试续约。超时时间我们让用户指定

•如果返回的是其它的 error，又要怎么办

答：我们只处理超时引起的续约失败，其它情况下，我们告诉用户遇到了无法处理的 error。

•第三个问题：如果确认续约失败了， 怎么中断后续的业务？

答：那就是这个问题基本无解，因为业务代码一旦执行，你除非自己手动检测分布式锁，并且手动中断，不然是没有办法的

***自动续约***

```go
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
			if err != nil { 
                //始终需要根据业务处理
                //根据err是否能解决，来判断是否需要通知其他业务的中断
				return err
			}
		case <-l.unlock:
			return nil
		}
	}
}
```

自动续约的可控性不高，所以如果要使用分布式锁，最好的是根据自己的需求来调用Refresh的api进而实现自动续约而不是使用AutoRefresh

### 4.加锁重试

加锁可能遇到偶发性的失败，在这种情况下，可以尝试重试。重试逻辑：

•如果超时了，则直接加锁

•检查一下 key 对应的值是不是我们刚才超时加锁请求的值，如果是，直接返回，前一次加锁成功了（这里你可能需要考虑重置一下过期时间）

•如果不是，直接返回，加锁失败

```go
func (c *Client) Lock(ctx context.Context, key string,
	expiration time.Duration, retry RetryStrategy, timeout time.Duration) (*Lock, error) {
	value := uuid.New().String()
	//计时器
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		lctx, cancel := context.WithTimeout(ctx, timeout)
		//尝试获得锁
		res, err := c.client.Eval(lctx, luaLock, []string{key}, value, expiration).Bool()
		cancel()

		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
            //不可挽回错误
			return nil, err
		}
		//如果是超时
		if res {
			return newLock(c.client, key, value, expiration), nil
		}
		interval, ok := retry.Next()
		if !ok {
			return nil, ErrFailedTOPreemptLock
		}
		//重试间隔，计时器
		if timer != nil {
			timer = time.NewTimer(interval)
		}
		timer.Reset(interval)
		select {
		case <-ctx.Done():
            //整个过程超时
			return nil, ctx.Err()
		case <-timer.C:

		}
	}
}
```

实际上，释放锁也可以考虑重试，只是相比之下，释放锁问题没那么严重。释放锁的情况下只有超时是值得重试的，其它情况都不需要重试。

**重试策略**

重试的接口设计成迭代器的形态。

用户可以轻易通过扩展这个接口来实现自己的重试策略，比如等时间间隔策略。

缺点就是这种接口设计没有引入上下文的概念，那么用户在实现接口的时候就没有办法根据上下文，例如上一次调用的 error 来决定要不要重试。

```go
type RetryStrategy interface {
	// Next 返回下一次重试的间隔，如果不需要继续重试，那么第二参数发挥 false
	Next() (time.Duration, bool)
}

type FixIntervalRetry struct {
	// 重试间隔
	Interval time.Duration
	// 最大次数
	Max int
	cnt int
}

func (f *FixIntervalRetry) Next() (time.Duration, bool) {
	f.cnt++
	return f.Interval, f.cnt <= f.Max
}

```

### 5.singleflight **优化**

在非常高并发，并且热点集中的情况下，可以考虑结合 singleflight 来进行优化。也就是说，本地所有的 goroutine 自己先竞争一把，胜利者再去抢全局的分布式锁。

**Redis 主从切换**

前面讨论的都是单点的 Redis，在集群部署的时候，需要额外考虑一个问题：主从切换。

**Redlock**

比如说我们部署五个主节点，那么加锁过程是类似的，只是要在五个主节点上都加上锁，如果多数（这里是三个）都成功了，那么就认为加锁成功。

### 6.总结

•使用分布式锁，你不能指望框架提供万无一失的方案，自己还是要处理各种异常情况（超时）

•自己写分布式锁，要考虑过期时间，以及要不要续约

•不管要对锁做什么操作，首先要确认这把锁是我们自己的锁

•大多数时候，与其选择复杂方案，不如直接让业务失败，可能成本还要低一点。（有时候直接赔钱，比你部署一大堆节点，招一大堆开发，搞好几个机房还要便宜，而且便宜很多）也就是选择恰好的方案，而不是完美的方案。

### 7.面试要点

•分布式锁怎么实现？核心就是 SetNX，只不过在我们引入重试之后，就需要使用 lua 脚本了。

•分布式锁的过期时间怎么设置？按照自己业务的耗时，例如 999 线的时间设置超时时间，重要的是要引入续约的机制。

•怎么延长过期时间（续约）？其实讨论的就是分布式锁续约的那几个问题，什么时候续约，续多长时间，续约失败怎么办？续约失败之后，无非就是中断业务执行，并且可能还要执行一些回滚或者补偿动作。

•分布式锁加锁失败有什么原因？超时、网络故障、Redis 服务器故障，以及锁被人持有着。

•怎么优化分布式锁的性能？根源上还是尽量避免使用分布式锁，硬要用那就可以考虑使用 singleflight 来进行优化。