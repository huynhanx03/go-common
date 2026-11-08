package redis

import (
	"context"

	redisV9 "github.com/redis/go-redis/v9"
)

// RunScript executes a reusable Lua script with namespaced keys.
//
// All keys accessed by the script must be supplied through keys. In Redis
// Cluster, the namespaced keys must share a hash slot. A transport error can
// leave the result ambiguous if Redis applied the script before the reply was
// lost; the caller must use an idempotent operation when retrying is unsafe.
func (r *RedisEngine) RunScript(ctx context.Context, script *redisV9.Script, keys []string, args ...any) (any, error) {
	if script == nil {
		return nil, ErrInvalidScript
	}

	scopedKeys := make([]string, len(keys))
	for i, key := range keys {
		scopedKeys[i] = r.scopedKey(key)
	}
	return r.runScopedScript(ctx, script, scopedKeys, args...).Result()
}

// runScopedScript executes a script using keys that already include this engine's namespace.
func (r *RedisEngine) runScopedScript(ctx context.Context, script *redisV9.Script, keys []string, args ...any) *redisV9.Cmd {
	return script.Run(ctx, r.client, keys, args...)
}

// queueScopedScript uses EVAL so a pipelined command does not depend on a prior script load.
func (r *RedisEngine) queueScopedScript(ctx context.Context, pipeline redisV9.Pipeliner, script *redisV9.Script, keys []string, args ...any) *redisV9.Cmd {
	return script.Eval(ctx, pipeline, keys, args...)
}

const cacheVersionScriptPrefix = `
	local maximum = '` + cacheVersionMaximum + `'
	local function read_version(key)
		local current = redis.call('GET', key)
		if not current then
			return '0', false
		end
		local length = string.len(current)
		if length == 0 or not string.match(current, '^%d+$') then
			return nil, true
		end
		if length > 1 and string.sub(current, 1, 1) == '0' then
			return nil, true
		end
		if length > string.len(maximum) or (length == string.len(maximum) and current > maximum) then
			return nil, true
		end
		return current, true
	end
`

func invalidCacheVersionReply() string {
	return `return redis.error_reply('` + cacheVersionInvalidCode + `')`
}

var (
	cacheSnapshotVersionScript = redisV9.NewScript(cacheVersionScriptPrefix + `
		local current, exists = read_version(KEYS[1])
		if not current then
			` + invalidCacheVersionReply() + `
		end
		if exists then
			redis.call('PERSIST', KEYS[1])
		end
		return current
`)

	cacheSetIfVersionScript = redisV9.NewScript(cacheVersionScriptPrefix + `
		local current = read_version(KEYS[1])
		if not current then
			` + invalidCacheVersionReply() + `
		end
		if current ~= ARGV[1] then
			return 0
		end
		redis.call('SET', KEYS[2], ARGV[2], 'PX', ARGV[3])
		return 1
`)

	cacheDeleteIfVersionScript = redisV9.NewScript(cacheVersionScriptPrefix + `
		local current = read_version(KEYS[1])
		if not current then
			` + invalidCacheVersionReply() + `
		end
		if current ~= ARGV[1] then
			return 0
		end
		return redis.call('DEL', KEYS[2])
`)

	cacheRevokeScript = redisV9.NewScript(cacheVersionScriptPrefix + `
		local current = read_version(KEYS[1])
		if not current then
			` + invalidCacheVersionReply() + `
		end
		local version = redis.call('INCR', KEYS[1])
		redis.call('PERSIST', KEYS[1])
		redis.call('DEL', KEYS[2], KEYS[3])
		return version
`)
)
