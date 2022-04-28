package server

import (
	"github.com/gin-gonic/gin"
	"net/http"
)


func NewServer() *gin.Engine {
	s := gin.Default()
	// static files
	s.Static("/static", "./static")
	s.StaticFS("/stat", http.Dir("./static"))
	s.StaticFile("/", "web/index.html")
	s.StaticFile("/refresh", "./web/refresh.html")
	s.StaticFile("/polling", "./web/polling.html")
	s.StaticFile("/ws", "./web/ws.html")

	{
		// websocket
		s.GET("/ws/socket", Websocket.Handle())
	}

	return s
}