package redis

import redisV9 "github.com/redis/go-redis/v9"

var (
	cacheSnapshotVersionScript = redisV9.NewScript(`
		local current = redis.call('GET', KEYS[1])
		if not current then
			return '0'
		end
		if string.match(current, '^%d+$') then
			redis.call('PERSIST', KEYS[1])
			return current
		end
		redis.call('SET', KEYS[1], '0')
		return '0'
`)

	cacheSetIfVersionScript = redisV9.NewScript(`
		local current = redis.call('GET', KEYS[1]) or '0'
		if current ~= ARGV[1] then
			return 0
		end
		redis.call('SET', KEYS[2], ARGV[2], 'PX', ARGV[3])
		return 1
`)

	cacheDeleteIfVersionScript = redisV9.NewScript(`
		local current = redis.call('GET', KEYS[1]) or '0'
		if current ~= ARGV[1] then
			return 0
		end
		return redis.call('DEL', KEYS[2])
`)

	cacheRevokeScript = redisV9.NewScript(`
		local current = redis.call('GET', KEYS[1])
		if current and not string.match(current, '^%d+$') then
			redis.call('SET', KEYS[1], '0')
		end
		local version = redis.call('INCR', KEYS[1])
		redis.call('PERSIST', KEYS[1])
		redis.call('DEL', KEYS[2], KEYS[3])
		return version
`)
)
