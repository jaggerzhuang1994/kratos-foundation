package redis

import (
	"time"

	"github.com/redis/go-redis/v9"
)

type Client = redis.Client
type Scanner = redis.Scanner

var Nil = redis.Nil
var KeepTTL = time.Duration(redis.KeepTTL)
