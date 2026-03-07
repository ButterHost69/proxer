package main

import (
	"context"

	"github.com/redis/go-redis/v9"
)

type RedisDB struct {
	db *redis.Client
}

func (r *RedisDB) Connect(addr string, passwd string) error {
	r.db = redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: passwd,
	})

	_, err := r.db.Ping(context.Background()).Result()

	return err
}
