package main

import (
	"github.com/232425wxy/explorer/server"
	"log"
)

func main() {
	s := server.NewServer()
	log.Fatal(s.Run())
}
