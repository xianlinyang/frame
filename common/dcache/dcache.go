package dcache

import (
	"github.com/xianlinyang/frame/common/log"
)

type Dcache interface {
	Check(key string) bool
	CheckMem(key string) bool
	Get(key string, data interface{}) bool
	Set(key string, data interface{}, ttl int) bool
	Delete(key string) bool
	ScanDelete(key string) (int, error)
	Incr(key string, data interface{}) bool
	IncrBy(key string, data int64) int64
	ZADD(key string, score float64, member interface{})
	ZRangeWithScores(key string, start, stop int64) []redis.Z
	ZRevRangeWithScores(key string, start, stop int64) []redis.Z
	Zrange(key string, start, stop int64) []string
	ZREM(key string, member interface{})
	Zcard(key string) int64
	SetNx(key string, value interface{}, tm int) (bool, error)
	GeoAdd(key string, geoLocation ...*redis.GeoLocation) (int64, error)
	GeoPos(key string, members ...string) ([]*redis.GeoPos, error)
	GeoDist(key string, member1, member2, unit string) (float64, error)
	GeoRadius(key string, longitude, latitude float64, query *redis.GeoRadiusQuery) ([]redis.GeoLocation, error)
	GeoRadiusByMember(key, member string, query *redis.GeoRadiusQuery) ([]redis.GeoLocation, error)
	GeoHash(key string, members ...string) ([]string, error)
}

var dcache Dcache

func Init(addrs []string, pass string) {
	log.Infof("初始化dcache %d", len(addrs))

	if len(addrs) > 1 {
		dcache = newRedisCluster(addrs, pass)
	} else if len(addrs) == 1 {
		log.Infof("初始化dcache %s", (addrs[0]))
		dcache = newRedis(addrs[0], pass)
	}
}

func Instance() Dcache {
	return dcache
}
