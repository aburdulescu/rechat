package main

import (
	"flag"
	"log"
	"net/http"
)

var listenAddr = flag.String("addr", ":8080", "http listen address")

func main() {
	flag.Parse()
	http.HandleFunc("/", handleRequest)
	log.Fatal(http.ListenAndServe(*listenAddr, nil))
}

func handleRequest(w http.ResponseWriter, r *http.Request) {
}
