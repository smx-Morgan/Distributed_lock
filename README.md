# Distributed_lock

Using redis to impl a distributed lock

分布式锁，简单来说就是在分布式环境下不同实例之间抢一把锁。

和普通的锁比起来，也就是抢锁的从线程（协程）变成了实例。

实现分布式锁的起点，就是利用setnx命令，确保可以排他地设置一个键值