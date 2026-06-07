package server

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
)

// RegisterFrontend registers the static file server for the frontend.
func RegisterFrontend(mux *http.ServeMux, devMode bool, frontendFS embed.FS) {
	if devMode {
		log.Println("开发模式：使用外部 frontend/ 文件夹")
		mux.Handle("/", http.FileServer(http.Dir("frontend")))
	} else {
		log.Println("生产模式：使用嵌入的前端文件")
		subFS, err := fs.Sub(frontendFS, "frontend")
		if err != nil {
			log.Fatalf("无法创建子文件系统: %v", err)
		}
		mux.Handle("/", http.FileServer(http.FS(subFS)))
	}
}
