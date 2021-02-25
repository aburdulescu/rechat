package main

import (
	"context"
	"flag"
	"log"
	"net/http"

	redis "github.com/go-redis/redis/v8"
	"github.com/gorilla/websocket"
)

const (
	pubsubChannel    = "chat"
	maxWSConnections = 1000
)

var listenAddr = flag.String("addr", ":8080", "http listen address")
var upgrader = websocket.Upgrader{}

func main() {
	flag.Parse()

	log.SetFlags(log.Lshortfile | log.Ltime | log.Lmicroseconds | log.LUTC)

	rdb := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
	})

	s := Server{
		rdb:         rdb,
		connections: make(chan WSConnData, maxWSConnections),
	}

	pubsubStop := make(chan struct{})
	defer func() {
		pubsubStop <- struct{}{} // TODO: this doesn't work
	}()

	go handlePubSub(s.rdb, s.connections, pubsubStop)

	http.HandleFunc("/", serveHome)
	http.HandleFunc("/ws", s.handleConnection)

	log.Fatal(http.ListenAndServe(*listenAddr, nil)) // TODO: stop this on CTRL-C
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

func (s Server) handleConnection(w http.ResponseWriter, r *http.Request) {
	clientAddr := r.RemoteAddr

	log.Println("new conn from", clientAddr)

	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("%s: upgrade: %v\n", clientAddr, err)
		return
	}
	defer c.Close()

	log.Printf("%s: send conn\n", clientAddr)
	s.connections <- WSConnData{c, true}
	log.Printf("%s: conn sent\n", clientAddr)

	for {
		mt, msg, err := c.ReadMessage()
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
	}

	s.connections <- WSConnData{c, false}
}

func handlePubSub(rdb *redis.Client, connectionsCh chan WSConnData, stopCh chan struct{}) {
	pubsub := rdb.Subscribe(context.Background(), pubsubChannel)
	if _, err := pubsub.Receive(context.Background()); err != nil {
		log.Println("pubsub.Receive: ", err)
		return
	}
	defer pubsub.Close()

	var connections []*websocket.Conn

	connectionsMap := make(map[string]int)

	pubsubCh := pubsub.Channel()

	log.Println("now will wait for things")

	for {
		select {
		case msg := <-pubsubCh:
			log.Println("pubsub: recv:", msg.Payload)
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
			if conn.isActive {
				connections = append(connections, conn.c)
				connectionsMap[connAddr] = len(connections) - 1
			} else {
				i, ok := connectionsMap[connAddr]
				if !ok {
					log.Println("pubsub: cannot find", connAddr)
					break
				}
				connections[i] = connections[len(connections)-1]
				connections[len(connections)-1] = nil
				connections = connections[:len(connections)-1]
			}
			log.Println("pubsub: connection handled:", conn.c.UnderlyingConn().RemoteAddr(), conn.isActive)
		case <-stopCh:
			log.Println("pubsub: done")
			return
		}
	}

	log.Println("pubsub: this should not happen")
}
