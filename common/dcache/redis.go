package dcache

import (
	"context"
	"encoding/json"
	"math/rand"
	"time"

	"github.com/xianlinyang/frame/common/log"
)

const (
	redis_item_timeout = 60 * 60
	redis_sync_chan    = "dcach_sync"
	redis_sync_set     = 1
	redis_sync_del     = 2
)

// 生成随机字符串
func GetRandomString(size int) string {
	str := "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	bytes := []byte(str)
	result := []byte{}
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < size; i++ {
		result = append(result, bytes[r.Intn(len(bytes))])
	}
	return string(result)
}

type publisher struct {
	From string
	Act  int
	Key  string
	Val  string
	Ttl  int
}

type RedisSession struct {
	client     *redis.Client
	clusterCLi *redis.ClusterClient
	ip         string
	pass       string
	name       string
	mem        *MemSession
	cluster    bool
}

func newRedis(ip string, pass string) *RedisSession {
	s := new(RedisSession)
	s.pass = pass
	s.name = GetRandomString(16)
	s.client = redis.NewClient(&redis.Options{
		Addr:     ip,
		Password: pass, // no password set
		DB:       0,
	})
	s.cluster = false
	s.mem = newMemSession()
	go s.subscribe()
	return s
}

func newRedisCluster(ip []string, pass string) *RedisSession {
	s := new(RedisSession)
	s.pass = pass
	s.name = GetRandomString(16)
	s.clusterCLi = redis.NewClusterClient(&redis.ClusterOptions{
		Addrs:    ip,
		Password: pass, // no password set
	})
	s.cluster = true
	s.mem = newMemSession()
	go s.subscribe()
	return s
}

var ctx = context.Background()

// 监听数据修改事件
func (this *RedisSession) subscribe() {
	var sub *redis.PubSub
	if this.cluster {
		this.clusterCLi.Subscribe(ctx, redis_sync_chan)
	} else {
		sub = this.client.Subscribe(ctx, redis_sync_chan)
	}
	defer sub.Close()
	var pub publisher
	for {
		msgi, err := sub.Receive(ctx)
		if err != nil {
			if err = sub.Ping(ctx, "ping"); err != nil {
				log.Error(err.Error())
				break
			}
		} else {
			switch msg := msgi.(type) {
			case *redis.Subscription:
				log.Debugf("subscribed to %s", msg.Channel)
			case *redis.Message:
				if e := json.Unmarshal([]byte(msg.Payload), &pub); e == nil {
					if pub.From != this.name {
						//log.Debug("received %s from %s ", msg.Payload, msg.Channel)
						//log.Debug("[ %s ]:message:", pub.From, msg.Payload)
						if pub.From != this.name {
							if pub.Act == redis_sync_set {
								this.mem.Set(pub.Key, pub.Val, pub.Ttl)
							} else if pub.Act == redis_sync_del {
								this.mem.Delete(pub.Key)
							}
						}
					}
				} else {
					log.Warnln(e.Error())
				}
			case *redis.Pong:
				log.Tracef("pong")
			default:
				log.Error("redis unreached", msgi)
			}
		}
	}
}

// 消息推送
func (this *RedisSession) publish(key, val string, ttl int, act int) {
	p := new(publisher)
	p.Key = key
	p.Val = val
	p.Ttl = ttl
	p.Act = act
	p.From = this.name

	//转为字符串
	s, e := json.Marshal(p)
	if e != nil {
		log.Warnln(e.Error())
		return
	}
	if this.cluster {
		rsp := this.clusterCLi.Publish(ctx, redis_sync_chan, string(s))
		if rsp.Err() != nil {
			log.Warnln(rsp.Err().Error())
		}
	} else {
		rsp := this.client.Publish(ctx, redis_sync_chan, string(s))
		if rsp.Err() != nil {
			log.Warnln(rsp.Err().Error())
		}
	}
}

// 获取超时
func (this *RedisSession) getTtl(key string) (int, bool) {
	var dur *redis.DurationCmd
	if this.cluster {
		dur = this.clusterCLi.TTL(ctx, key)
	} else {
		dur = this.client.TTL(ctx, key)
	}
	return int(dur.Val().Seconds()), true
}

func (this *RedisSession) Get(key string, data interface{}) bool {
	var s string
	if !this.mem.Get(key, &s) {
		var str *redis.StringCmd
		if this.cluster {
			str = this.clusterCLi.Get(ctx, key)
		} else {
			str = this.client.Get(ctx, key)
		}
		if str.Err() != nil {
			log.Warnf("获取key %s 失败, %s", key, str.Err().Error())
			return false
		}
		s = str.Val()
		if ttl, ok := this.getTtl(key); ok {
			log.Debugf("load: %s ttl[ %d ] from redis:", str.Val(), ttl)
			if e := json.Unmarshal([]byte(str.Val()), data); e != nil {
				log.Warnln(e.Error())
				return false
			}
			//内存提前5秒超时
			return this.mem.Set(key, s, ttl-5)
		}
		return false
	}
	if e := json.Unmarshal([]byte(s), data); e != nil {
		log.Warnln("%s - %s ", s, e.Error())
		return false
	}
	return true
}

func (this *RedisSession) Set(key string, data interface{}, ttl int) bool {
	var rsp *redis.StatusCmd
	//转为字符串
	s, e := json.Marshal(data)
	if e != nil {
		log.Warnln(e.Error())
		return false
	}
	//必须配置超时
	if ttl <= 0 {
		ttl = redis_item_timeout
	}
	if this.cluster {
		rsp = this.clusterCLi.Set(ctx, key, s, time.Second*time.Duration(ttl))
	} else {
		rsp = this.client.Set(ctx, key, s, time.Second*time.Duration(ttl))
	}
	if rsp.Err() != nil {
		log.Warnln(rsp.Err().Error())
	} else {
		//缓存本地
		if this.mem != nil {
			this.mem.Set(key, string(s), ttl)
		}
		//通告修改
		go this.publish(key, string(s), ttl, redis_sync_set)
		return true
	}
	return false
}

func (this *RedisSession) Delete(key string) bool {
	var rsp *redis.IntCmd
	if this.cluster {
		rsp = this.clusterCLi.Del(ctx, key)
	} else {
		rsp = this.client.Del(ctx, key)
	}
	if rsp.Err() != nil {
		log.Warn("删除", key, "错误:", rsp.Err().Error())
	}
	//缓存本地
	if this.mem != nil {
		this.mem.Delete(key)
	}
	//通告删除
	go this.publish(key, "", 0, redis_sync_del)
	return true
}

func (this *RedisSession) ScanDelete(key string) (int, error) {
	var cursor uint64
	var n int
	var err error
	for {
		var keys []string
		//扫描key，每次100条
		if this.cluster {
			keys, cursor, err = this.clusterCLi.Scan(ctx, cursor, key, 100).Result()
		} else {
			keys, cursor, err = this.client.Scan(ctx, cursor, key, 100).Result()
		}
		if err != nil {
			log.Error("scan", key, "错误:", err.Error())
			return 0, err
		}
		n += len(keys)
		for _, v := range keys {
			if this.cluster {
				_, err = this.clusterCLi.Del(ctx, v).Result()
			} else {
				_, err = this.client.Del(ctx, v).Result()
			}
			if err != nil {
				log.Error("删除", key, "错误:", err.Error())
				return n, err
			}
		}
		if cursor == 0 {
			break
		}
	}

	return n, nil
}

func (this *RedisSession) Incr(key string, data interface{}) bool {
	var rsp *redis.IntCmd
	if this.cluster {
		rsp = this.clusterCLi.Incr(ctx, key)
	} else {
		rsp = this.client.Incr(ctx, key)
	}
	if rsp.Err() != nil {
		log.Warnln("Incr key %s 失败, %s", key, rsp.Err().Error())
		return false
	}
	*data.(*int64) = rsp.Val()
	return true
}

func (this *RedisSession) IncrBy(key string, data int64) int64 {
	var rsp *redis.IntCmd
	if this.cluster {
		rsp = this.clusterCLi.IncrBy(ctx, key, data)
	} else {
		rsp = this.client.IncrBy(ctx, key, data)
	}
	if rsp.Err() != nil {
		log.Warnf("Incrby key %s 失败, %s", key, rsp.Err().Error())
	}
	return rsp.Val()
}

func (this *RedisSession) Check(key string) bool {
	var rsp *redis.IntCmd
	if this.cluster {
		rsp = this.clusterCLi.Exists(ctx, key)
	} else {
		rsp = this.client.Exists(ctx, key)
	}
	if rsp.Val() == 1 {
		return true
	}
	return false
}

func (this *RedisSession) CheckMem(key string) bool {
	return this.mem.Check(key)
}

// ZADD 添加到有序集合里面 比如存用户就是 zadd user 1 1001
func (this *RedisSession) ZADD(key string, score float64, member interface{}) {
	data := &redis.Z{
		Score:  score,
		Member: member,
	}
	var rsp *redis.IntCmd
	if this.cluster {
		rsp = this.clusterCLi.ZAdd(ctx, key, data)
	} else {
		rsp = this.client.ZAdd(ctx, key, data)
	}
	if rsp.Err() != nil {
		log.Warnf("ZADD key %s 失败, %s", key, rsp.Err().Error())
	}
}

// Zrange 遍历有序集合
func (this *RedisSession) Zrange(key string, start, stop int64) []string {
	//data := &redis.StringSliceCmd{}
	var rsp *redis.StringSliceCmd
	if this.cluster {
		rsp = this.clusterCLi.ZRange(ctx, key, start, stop)
	} else {
		rsp = this.client.ZRange(ctx, key, start, stop)
	}
	if rsp.Err() != nil {
		log.Warnln("ZADD key %s 失败, %s", key, rsp.Err().Error())
		return nil
	}
	//fmt.Println("rsp.Val() :::::: ",rsp.Val())
	return rsp.Val()
}

// ZRangeWithScores 遍历有序集合
func (this *RedisSession) ZRangeWithScores(key string, start, stop int64) []redis.Z {
	//data := &redis.StringSliceCmd{}
	var rsp *redis.ZSliceCmd
	if this.cluster {
		rsp = this.clusterCLi.ZRangeWithScores(ctx, key, start, stop)
	} else {
		rsp = this.client.ZRangeWithScores(ctx, key, start, stop)
	}
	if rsp.Err() != nil {
		log.Warnln("ZADD key %s 失败, %s", key, rsp.Err().Error())
		return nil
	}
	//fmt.Println("rsp.Val() :::::: ",rsp.Val())
	return rsp.Val()
}

// ZRangeWithScores 遍历有序集合
func (this *RedisSession) ZRevRangeWithScores(key string, start, stop int64) []redis.Z {
	//data := &redis.StringSliceCmd{}
	var rsp *redis.ZSliceCmd
	if this.cluster {
		rsp = this.clusterCLi.ZRevRangeWithScores(ctx, key, start, stop)
	} else {
		rsp = this.client.ZRevRangeWithScores(ctx, key, start, stop)
	}
	if rsp.Err() != nil {
		log.Warnln("ZADD key %s 失败, %s", key, rsp.Err().Error())
		return nil
	}
	//fmt.Println("rsp.Val() :::::: ",rsp.Val())
	return rsp.Val()
}

// ZREM 删除有序集合中某个元素
func (this *RedisSession) ZREM(key string, member interface{}) {
	//data := &redis.StringSliceCmd{}
	var rsp *redis.IntCmd
	if this.cluster {
		rsp = this.clusterCLi.ZRem(ctx, key, member)
	} else {
		rsp = this.client.ZRem(ctx, key, member)
	}
	if rsp.Err() != nil {
		log.Warnln("Zrem key %s 失败, %s", key, rsp.Err().Error())
		return
	}
	return
}

// Zcard 返回集合数
func (this *RedisSession) Zcard(key string) int64 {
	//data := &redis.StringSliceCmd{}
	var rsp *redis.IntCmd
	if this.cluster {
		rsp = this.clusterCLi.ZCard(ctx, key)
	} else {
		rsp = this.client.ZCard(ctx, key)
	}
	if rsp.Err() != nil {
		log.Warnln("Zrem key %s 失败, %s", key, rsp.Err().Error())
		return 0
	}
	return rsp.Val()
}

//SetNx 设置nx
/*
* 如果不存在相关的key,value 则设置,否则不设置
* 参数说明:
* @param:key   redis中的key
* @param:value redis中的value
* @param:tm 	redis中的超时
 */
func (this *RedisSession) SetNx(key string, value interface{}, tm int) (bool, error) {
	if this.cluster {
		return this.clusterCLi.SetNX(ctx, key, value, time.Second*time.Duration(tm)).Result()
	} else {
		return this.client.SetNX(ctx, key, value, time.Second*time.Duration(tm)).Result()
	}
}

// Geo 设置
// GeoAdd 增加某个位置的坐标
func (this *RedisSession) GeoAdd(key string, geoLocation ...*redis.GeoLocation) (int64, error) {

	if this.cluster {
		return this.clusterCLi.GeoAdd(ctx, key, geoLocation...).Result()
	} else {
		return this.client.GeoAdd(ctx, key, geoLocation...).Result()
	}

}

// GeoPos 获取某个位置的坐标
func (this *RedisSession) GeoPos(key string, members ...string) ([]*redis.GeoPos, error) {
	if this.cluster {
		return this.clusterCLi.GeoPos(ctx, key, members...).Result()
	} else {
		return this.client.GeoPos(ctx, key, members...).Result()
	}
}

// GeoDist 获取两个位置的距离
/*其中 unit 参数是距离单位
* m 表示单位为米。
* km 表示单位为千米。
* mi 表示单位为英里。
* ft 表示单位为英尺。
 */
func (this *RedisSession) GeoDist(key string, member1, member2, unit string) (float64, error) {
	if this.cluster {
		return this.clusterCLi.GeoDist(ctx, key, member1, member2, unit).Result()
	} else {
		return this.client.GeoDist(ctx, key, member1, member2, unit).Result()
	}

}

// GeoRadius 根据给定位置坐标获取指定范围内的位置集合
func (this *RedisSession) GeoRadius(key string, longitude, latitude float64, query *redis.GeoRadiusQuery) ([]redis.GeoLocation, error) {

	if this.cluster {
		return this.clusterCLi.GeoRadius(ctx, key, longitude, latitude, query).Result()
	} else {
		return this.client.GeoRadius(ctx, key, longitude, latitude, query).Result()
	}
}

// GeoRadiusByMember 根据给定位置获取指定范围内的位置集合
func (this *RedisSession) GeoRadiusByMember(key, member string, query *redis.GeoRadiusQuery) ([]redis.GeoLocation, error) {

	if this.cluster {
		return this.clusterCLi.GeoRadiusByMember(ctx, key, member, query).Result()
	} else {
		return this.client.GeoRadiusByMember(ctx, key, member, query).Result()
	}

}

func (this *RedisSession) GeoHash(key string, members ...string) ([]string, error) {

	if this.cluster {
		return this.clusterCLi.GeoHash(ctx, key, members...).Result()
	} else {
		return this.client.GeoHash(ctx, key, members...).Result()
	}

}
