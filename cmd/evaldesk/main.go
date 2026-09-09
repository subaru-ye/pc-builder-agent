// evaldesk reads saved artifacts locally; it never initializes production services.
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/evaldesk"
)

func main() {
	root := flag.String("root", ".", "repository root containing artifacts/eval and artifacts/evalchange")
	address := flag.String("addr", "127.0.0.1:8086", "loopback listen address")
	flag.Parse()
	if !evaldesk.ListenAddress(*address) {
		log.Fatal("evaldesk 仅允许 localhost / loopback 监听地址")
	}
	store, err := evaldesk.NewStore(*root)
	if err != nil {
		log.Fatal("无法读取项目根目录")
	}
	server := &http.Server{Addr: *address, Handler: evaldesk.Handler(store), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	log.Printf("评估只读服务已启动：http://%s；前端入口 /eval", *address)
	log.Fatal(server.ListenAndServe())
}
