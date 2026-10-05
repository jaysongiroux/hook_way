package protectionstore

// All keys share one hash tag. No automatic command retry is permitted: a
// transport failure can occur after this script has debited/reserved state.
const redisScript = `
local op=ARGV[1]
local p=cjson.decode(ARGV[2])
local fp=ARGV[3]
local key=ARGV[4]
local owner=ARGV[5]
local expected=ARGV[6]
local batch=tonumber(ARGV[7])
local clock=redis.call('TIME')
local now=tonumber(clock[1])*1000+math.floor(tonumber(clock[2])/1000)
if op=='probe' then
 redis.call('SET',KEYS[1],'probe');redis.call('GET',KEYS[1])
 redis.call('HSET',KEYS[2],'probe','1');redis.call('HGET',KEYS[2],'probe')
 redis.call('HLEN',KEYS[2]);redis.call('ZADD',KEYS[4],0,'probe');redis.call('ZRANGEBYLEX',KEYS[4],'-','+','LIMIT',0,1)
 redis.call('ZADD',KEYS[3],1,'probe');redis.call('ZRANGEBYSCORE',KEYS[3],0,1,'LIMIT',0,1)
 redis.call('HDEL',KEYS[2],'probe');redis.call('ZREM',KEYS[3],'probe');redis.call('DEL',KEYS[1],KEYS[2],KEYS[3],KEYS[4]);return {'ok'}
end
local raw=redis.call('GET',KEYS[1])
if not raw then
 if op=='ensure' then redis.call('SET',KEYS[1],cjson.encode({policy=p,fingerprint=fp}));return {'ok'} end
 return {'conflict'}
end
local rec=cjson.decode(raw)
if op=='status' then return {'ok',raw} end
local function clean()
 local expired=redis.call('ZRANGEBYSCORE',KEYS[3],'-inf',now,'LIMIT',0,batch)
 for _,k in ipairs(expired) do redis.call('HDEL',KEYS[2],k);redis.call('ZREM',KEYS[3],k);redis.call('ZREM',KEYS[4],k) end
 return #expired
end
local function advance(policy,s,t)
 t=math.max(t,s.at)
 if policy.strategy=='token_bucket' then local capacity=policy.burst*60000;local delta=math.min(t-s.at,math.ceil(capacity/policy.rpm));s.tokens=math.min(capacity,s.tokens+delta*policy.rpm) end
 if policy.strategy=='sliding_window_counter' then
  local w=policy.window/1000000;local start=t-t%w
  if start>s.window_start then if start-s.window_start==w then s.previous=s.current else s.previous=0 end;s.current=0;s.window_start=start end
 end
 s.at=t
end
local function expiry(policy,s)
 if policy.strategy=='token_bucket' then return s.at+math.ceil(policy.burst*60000/policy.rpm) end
 return s.window_start+2*policy.window/1000000
end
local function put(k,s) redis.call('HSET',KEYS[2],k,cjson.encode(s));redis.call('ZADD',KEYS[3],s.expires_at,k);redis.call('ZADD',KEYS[4],0,k) end
if op=='update' then
 if rec.fingerprint~=expected then return {'conflict'} end
 local old=rec.policy
 if old.kind~=p.kind or old.id~=p.id or old.key_scope~=p.key_scope or old.strategy~=p.strategy or old.window~=p.window then return {'conflict'} end
 if rec.pending then
  if rec.target_fingerprint~=fp then return {'conflict'} end
 else
  clean();if redis.call('HLEN',KEYS[2])>p.max_entries then return {'capacity'} end
  rec.pending=p;rec.target_fingerprint=fp;rec.update_at=now;rec.cursor=''
  redis.call('SET',KEYS[1],cjson.encode(rec))
 end
 if p.kind~='replay_guard' then
  local lower='-';if rec.cursor~='' then lower='('..rec.cursor end
  local scan=redis.call('ZRANGEBYLEX',KEYS[4],lower,'+','LIMIT',0,100)
  for _,k in ipairs(scan) do
   local s=cjson.decode(redis.call('HGET',KEYS[2],k))
   if s.version~=fp then advance(old,s,rec.update_at);s.tokens=math.min(s.tokens,p.burst*60000);s.expires_at=expiry(p,s);s.version=fp;put(k,s) end
   rec.cursor=k
  end
  rec.done=#scan<100
 end
 if p.kind=='replay_guard' or rec.done then rec.policy=p;rec.fingerprint=fp;rec.pending=nil;rec.target_fingerprint=nil;rec.cursor=nil;rec.update_at=nil;rec.done=nil;redis.call('SET',KEYS[1],cjson.encode(rec));return {'ok',1} end
 redis.call('SET',KEYS[1],cjson.encode(rec));return {'ok',0}
end
if rec.pending or rec.fingerprint~=fp then return {'conflict'} end
if op=='ensure' or op=='check_policy' then return {'ok'} end
if op=='gc' then return {'ok',clean()} end
clean()
local item=redis.call('HGET',KEYS[2],key)
local s=nil;if item then s=cjson.decode(item) end
if op=='release' then
 if s and owner~='' and s.owner==owner then redis.call('HDEL',KEYS[2],key);redis.call('ZREM',KEYS[3],key);redis.call('ZREM',KEYS[4],key) end
 return {'ok'}
end
if op=='reserve' then
 if s and s.expires_at>now then return {'ok',0,s.expires_at} end
 if s then redis.call('HDEL',KEYS[2],key);redis.call('ZREM',KEYS[3],key);redis.call('ZREM',KEYS[4],key);s=nil end
 if redis.call('HLEN',KEYS[2])>=p.max_entries then return {'capacity'} end
 s={owner=owner,expires_at=now+p.ttl/1000000,version=fp};put(key,s);return {'ok',1,s.expires_at}
end
if op=='allow' then
 if not s then
  if redis.call('HLEN',KEYS[2])>=p.max_entries then return {'capacity'} end
  local w=math.max(1,p.window/1000000);s={tokens=p.burst*60000,at=now,current=0,previous=0,window_start=now-now%w,owner='',version=fp}
 end
 advance(p,s,now)
 local allowed=0;local remaining=0;local retry=0
 if p.strategy=='token_bucket' then
  if s.tokens>=60000 then s.tokens=s.tokens-60000;allowed=1 end;remaining=math.floor(s.tokens/60000)
  if allowed==0 then retry=math.ceil((60000-s.tokens)/p.rpm) end
 else
  local w=p.window/1000000;local used=s.current+s.previous*(1-(s.at-s.window_start)/w)
  if used+1<=p.limit then s.current=s.current+1;used=used+1;allowed=1 end
  remaining=math.max(0,math.floor(p.limit-used));if allowed==0 then retry=s.window_start+2*w-s.at end
 end
 s.expires_at=expiry(p,s);put(key,s);return {'ok',allowed,remaining,retry}
end
return {'conflict'}
`
