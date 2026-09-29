// Command waveguide-service 是矩形波导模式核算服务的入口。
// 服务启动后固定监听 8080 端口。
package main

import (
	"log"

	"github.com/gin-gonic/gin"

	"waveguide-service/internal/api"
	"waveguide-service/internal/service"
	"waveguide-service/internal/store"
)

const listenAddr = ":8080"

func main() {
	gin.SetMode(gin.ReleaseMode)

	// 进程内并发安全存储，构造时已内置 WR-90 样例档案。
	st := store.NewMemoryStore()
	svc := service.New(st)
	router := api.NewRouter(svc)

	log.Printf("waveguide service listening on %s", listenAddr)
	if err := router.Run(listenAddr); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}
