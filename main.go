package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"time"

	redis "github.com/go-redis/redis/v8"
	"github.com/gorilla/websocket"
)

const (
	pubsubChannel = "chat"
	historyList   = "history"

	maxWSConnections = 1000
)

var listenAddr = flag.String("addr", ":8080", "http listen address")
var upgrader = websocket.Upgrader{}

func main() {
	flag.Parse()

	log.SetFlags(log.Lshortfile | log.Ltime | log.Lmicroseconds | log.LUTC)

	defer printGoroutines()

	var doneWg sync.WaitGroup
	doneWg.Add(1) // http server
	doneWg.Add(1) // pubsub
	defer func() {
		doneWg.Wait()
		log.Println("shutdown done")
	}()

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt)

	rdb := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
	})
	defer rdb.Close()

	s := Server{
		rdb:         rdb,
		connections: make(chan WSConnData, maxWSConnections),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", serveHome)
	mux.HandleFunc("/ws", s.handleConnection)
	srv := http.Server{
		Addr:    *listenAddr,
		Handler: mux,
	}
	go func() {
		defer doneWg.Done()
		log.Println("http server listening on", *listenAddr)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Println("srv.ListenAndServe:", err)
		}
		log.Println("http server stopped")
	}()

	pubsubCtx, pubsubCancel := context.WithCancel(context.Background())
	defer pubsubCancel()
	go handlePubSub(pubsubCtx, s.rdb, s.connections, &doneWg)

	<-done

	httpCtx, httpCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer httpCancel()

	if err := srv.Shutdown(httpCtx); err != nil {
		log.Println("srv.Shutdown", err)
	}
}

func printGoroutines() {
	ngoroutines := runtime.NumGoroutine()
	fmt.Println("num goroutines:", ngoroutines)

	b := make([]byte, ngoroutines*1024)
	n := runtime.Stack(b, true)
	fmt.Print(string(b[:n]))
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

type WSConnData struct {
	c        *websocket.Conn
	isActive bool
}

type Server struct {
	rdb         *redis.Client
	connections chan WSConnData
}

func (s Server) sendHistory(c *websocket.Conn) error {
	cmd := s.rdb.LRange(context.Background(), historyList, 0, -1)

	err := cmd.Err()
	if err != nil {
		log.Println("redis.LRange:", err)
		return err
	}

	history := cmd.Val()

	for _, msg := range history {
		if err := c.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
			log.Println("websocket.Write:", err)
			continue
		}
	}

	return nil
}

func (s Server) handleConnection(w http.ResponseWriter, r *http.Request) {
	clientAddr := r.RemoteAddr

	log.Println("new conn from", clientAddr)

	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("%s: upgrade: %v\n", clientAddr, err)
		return
	}
	defer c.Close()

	if err := s.sendHistory(c); err != nil {
		return
	}

	log.Printf("%s: send conn\n", clientAddr)
	s.connections <- WSConnData{c, true}
	log.Printf("%s: conn sent\n", clientAddr)

	for {
		mt, msg, err := c.ReadMessage() // TODO: this blocks goroutine and is leaked!!!
		if err != nil {
			log.Printf("%s: websocket.Read: %v\n", clientAddr, err)
			break
		}
		if mt != websocket.TextMessage {
			log.Printf("%s: message type %d not websocket.TextMessage\n", clientAddr, mt)
			continue
		}
		log.Printf("%s: ws: recv: %s", clientAddr, msg)
		if err := s.rdb.Publish(context.Background(), pubsubChannel, string(msg)).Err(); err != nil {
			log.Printf("%s: redis.Publish: %v\n", clientAddr, err)
			break
		}
		log.Printf("%s: redis: publish ok", clientAddr)
		if err := s.rdb.RPush(context.Background(), historyList, string(msg)).Err(); err != nil {
			log.Printf("%s: redis.RPush: %v\n", clientAddr, err)
			break
		}
	}

	s.connections <- WSConnData{c, false}
}

func handlePubSub(ctx context.Context, rdb *redis.Client, connectionsCh chan WSConnData, doneWg *sync.WaitGroup) {
	defer doneWg.Done()

	pubsub := rdb.Subscribe(context.Background(), pubsubChannel)
	if _, err := pubsub.Receive(context.Background()); err != nil {
		log.Println("pubsub.Receive: ", err)
		return
	}
	defer pubsub.Close()

	var connections []*websocket.Conn

	pubsubCh := pubsub.Channel()

	for {
		select {
		case msg := <-pubsubCh:
			log.Println("pubsub: recv:", msg.Payload)
			log.Println("no. connections:", len(connections))
			for _, c := range connections {
				log.Printf("pubsub: send msg to %s\n", c.UnderlyingConn().RemoteAddr())
				if err := c.WriteMessage(websocket.TextMessage, []byte(msg.Payload)); err != nil {
					log.Println("websocket.Write:", err)
					continue
				}
				log.Printf("pubsub: sent msg to %s\n", c.UnderlyingConn().RemoteAddr())
			}
		case conn := <-connectionsCh:
			connAddr := conn.c.UnderlyingConn().RemoteAddr().String()
			log.Println("pubsub: new connection:", connAddr, conn.isActive)
			log.Println("no. connections:", len(connections))
			if conn.isActive {
				connections = append(connections, conn.c)
			} else {
				i := findConn(connections, conn.c)
				if i == -1 {
					log.Println("pubsub: cannot find", connAddr)
					continue
				}
				copy(connections[i:], connections[i+1:])
				connections[len(connections)-1] = nil
				connections = connections[:len(connections)-1]
			}
			log.Println("pubsub: connection handled:", connAddr, conn.isActive)
			log.Println("no. connections:", len(connections))
		case <-ctx.Done():
			for _, c := range connections {
				c.Close()
			}
			log.Println("pubsub: done")
			return
		}
	}
}

func findConn(connections []*websocket.Conn, conn *websocket.Conn) int {
	for i, c := range connections {
		if c.UnderlyingConn().RemoteAddr() == conn.UnderlyingConn().RemoteAddr() {
			return i
		}
	}
	return -1
}
