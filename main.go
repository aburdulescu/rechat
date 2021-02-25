package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"sync"

	redis "github.com/go-redis/redis/v8"
	"github.com/gorilla/websocket"
)

const (
	pubsubChannel = "chat"
)

var listenAddr = flag.String("addr", ":8080", "http listen address")
var upgrader = websocket.Upgrader{}
var ctx = context.Background()

func main() {
	flag.Parse()
	http.HandleFunc("/", serveHome)
	http.HandleFunc("/ws", serveWS)
	log.Fatal(http.ListenAndServe(*listenAddr, nil))
}

func serveHome(w http.ResponseWriter, r *http.Request) {
	log.Println(r.URL)
	if r.URL.Path != "/" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	http.ServeFile(w, r, "index.html")
}

func serveWS(w http.ResponseWriter, r *http.Request) {
	clientAddr := r.RemoteAddr

	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("%s: upgrade: %v\n", clientAddr, err)
		return
	}
	defer c.Close()

	wsconn := NewWSConn(c)

	rdb := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
	})

	pubsub := rdb.Subscribe(ctx, pubsubChannel)
	if _, err := pubsub.Receive(ctx); err != nil {
		log.Printf("%s: pubsub.Receive: %v\n", clientAddr, err)
		return
	}
	defer pubsub.Close()

	pubsubStopCh := make(chan struct{})

	go func(ws *WSConn, pubsubCh <-chan *redis.Message, stopCh chan struct{}) {
		select {
		case msg := <-pubsubCh:
			log.Printf("%s: pubsub: recv: %v\n", clientAddr, msg.Payload)
			if err = ws.Write([]byte(msg.Payload)); err != nil {
				log.Printf("%s: websocket.Write: %v\n", clientAddr, err)
				break
			}
		case <-stopCh:
			log.Println("pubsub done")
			return
		}
	}(wsconn, pubsub.Channel(), pubsubStopCh)

	for {
		mt, msg, err := wsconn.Read()
		if err != nil {
			log.Printf("%s: websocket.Read: %v\n", clientAddr, err)
			break
		}
		if mt != websocket.TextMessage {
			log.Printf("%s: message type %d not websocket.TextMessage\n", clientAddr, mt)
			continue
		}
		log.Printf("ws: recv: %s", msg)
		if err := rdb.Publish(ctx, pubsubChannel, string(msg)).Err(); err != nil {
			log.Printf("%s: redis.Publish: %v\n", clientAddr, err)
			break
		}
	}
	pubsubStopCh <- struct{}{}
}

type WSConn struct {
	mu sync.Mutex
	c  *websocket.Conn
}

func NewWSConn(c *websocket.Conn) *WSConn {
	wsc := &WSConn{}
	wsc.c = c
	return wsc
}

func (c *WSConn) Read() (messageType int, p []byte, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.c.ReadMessage()
}

func (c *WSConn) Write(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.c.WriteMessage(websocket.TextMessage, data)
}
