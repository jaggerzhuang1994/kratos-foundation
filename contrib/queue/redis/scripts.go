package redis

// 所有状态转换在 Redis 单线程 Lua 中完成；不在 Go 进程内持锁。
// Lua 报错不会回滚已执行的写入，因此必须在修改前检查所有共享键的类型。
const validateKeysScript = `
local types = {'hash', 'list', 'zset', 'zset', 'zset'}
for i, expected in ipairs(types) do
 local actual = redis.call('TYPE', KEYS[i]).ok
 if actual ~= 'none' and actual ~= expected then
  return redis.error_reply('queue key type mismatch at index ' .. i)
 end
end
`

const enqueueScript = validateKeysScript + `
if redis.call('HEXISTS', KEYS[1], ARGV[1]) == 1 then return 0 end
redis.call('HSET', KEYS[1], ARGV[1], ARGV[2])
redis.call('ZADD', KEYS[3], ARGV[3], ARGV[1])
return 1
`

const reserveScript = validateKeysScript + `
for _, key in ipairs({KEYS[3], KEYS[4]}) do
 local ids = redis.call('ZRANGEBYSCORE', key, '-inf', ARGV[1], 'LIMIT', 0, 100)
 for _, id in ipairs(ids) do
  redis.call('RPUSH', KEYS[2], id)
  redis.call('ZREM', key, id)
 end
end
local id = redis.call('LINDEX', KEYS[2], 0)
if not id then return nil end
local raw = redis.call('HGET', KEYS[1], id)
local ok, data = pcall(cjson.decode, raw or '')
if not ok or type(data) ~= 'table' or type(data.task) ~= 'string' or type(data.attempts) ~= 'number' then
 redis.call('ZADD', KEYS[5], ARGV[1], id)
 redis.call('LPOP', KEYS[2])
 return redis.error_reply('corrupt queue record quarantined')
end
data.attempts = data.attempts + 1
data.token = ARGV[3]
local encoded = cjson.encode(data)
local result = cjson.encode({id=id, record=data})
redis.call('HSET', KEYS[1], id, encoded)
redis.call('ZADD', KEYS[4], ARGV[2], id)
redis.call('LPOP', KEYS[2])
return result
`

const transitionScript = validateKeysScript + `
local raw = redis.call('HGET', KEYS[1], ARGV[1])
if not raw or not redis.call('ZSCORE', KEYS[4], ARGV[1]) then return 0 end
local data = cjson.decode(raw)
if data.token ~= ARGV[2] then return 0 end
if ARGV[3] == 'ack' then
 redis.call('HDEL', KEYS[1], ARGV[1])
else
 data.token = ''
 if ARGV[3] == 'release' then
  local task = cjson.decode(data.task)
  task.AvailableAt = ARGV[6]
  data.task = cjson.encode(task)
  local encoded = cjson.encode(data)
  redis.call('ZADD', KEYS[3], ARGV[4], ARGV[1])
  redis.call('HSET', KEYS[1], ARGV[1], encoded)
 else
  data.reason = ARGV[5]
  data.failed_at = tonumber(ARGV[4])
  local encoded = cjson.encode(data)
  redis.call('ZADD', KEYS[5], ARGV[4], ARGV[1])
  redis.call('HSET', KEYS[1], ARGV[1], encoded)
 end
end
redis.call('ZREM', KEYS[4], ARGV[1])
return 1
`

const failedScript = validateKeysScript + `
local ids = redis.call('ZRANGE', KEYS[5], 0, tonumber(ARGV[1])-1)
local values = {}
for _, id in ipairs(ids) do
 local value = redis.call('HGET', KEYS[1], id)
 if not value then return redis.error_reply('missing failed queue record') end
 table.insert(values, value)
end
return values
`

const retryScript = validateKeysScript + `
if not redis.call('ZSCORE', KEYS[5], ARGV[1]) then return 0 end
local raw = redis.call('HGET', KEYS[1], ARGV[1])
if not raw then return redis.error_reply('missing failed queue record') end
local data = cjson.decode(raw)
local task = cjson.decode(data.task)
task.AvailableAt = ARGV[3]
data.task = cjson.encode(task)
data.attempts = 0
data.token = ''
data.reason = ''
data.failed_at = 0
redis.call('HSET', KEYS[1], ARGV[1], cjson.encode(data))
redis.call('ZADD', KEYS[3], ARGV[2], ARGV[1])
redis.call('ZREM', KEYS[5], ARGV[1])
return 1
`
