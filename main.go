package main

import (
	"flag"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var listenAddr = flag.String("addr", ":8080", "http listen address")
var upgrader = websocket.Upgrader{}

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
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Print("upgrade:", err)
		return
	}
	defer c.Close()
	// TODO: add goroutune that reads redis pubsub messages and sends them to websocket
	wsconn := NewWSConn(c)
	for {
		mt, message, err := wsconn.Read()
		if err != nil {
			log.Println("read:", err)
			break
		}
		log.Printf("recv: %s", message)
		if err = wsconn.Write(mt, message); err != nil {
			log.Println("write:", err)
			break
		}
	}
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

func (c *WSConn) Write(messageType int, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.c.WriteMessage(messageType, data)
}
