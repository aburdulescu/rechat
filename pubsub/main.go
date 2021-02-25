package main

import (
	"context"
	"fmt"
	"os"

	redis "github.com/go-redis/redis/v8"
)

var ctx = context.Background()

func main() {
	rdb := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
	})

	switch os.Args[1] {
	case "pub":
		if err := rdb.Publish(ctx, "news", os.Args[2]).Err(); err != nil {
			panic(err)
		}
	case "sub":
		pubsub := rdb.Subscribe(ctx, "news")
		if _, err := pubsub.Receive(ctx); err != nil {
			panic(err)
		}
		ch := pubsub.Channel()
		for msg := range ch {
			fmt.Println(msg.Channel, msg.Payload)
			if msg.Payload == "exit" {
				pubsub.Close()
			}
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown option")
		os.Exit(1)
	}
}
