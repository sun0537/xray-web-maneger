package server

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
)

// RegisterFrontend registers the static file server for the frontend.
func RegisterFrontend(mux *http.ServeMux, devMode bool, frontendFS embed.FS) {
	var handler http.Handler

	if devMode {
		log.Println("开发模式：使用外部 frontend/ 文件夹")
		handler = http.FileServer(http.Dir("frontend"))
	} else {
		log.Println("生产模式：使用嵌入的前端文件")
		// embed.FS + http.FS already reject "." / ".." / absolute paths,
		// so the embedded FS is safe against directory traversal by design.
		subFS, err := fs.Sub(frontendFS, "frontend")
		if err != nil {
			log.Fatalf("无法创建子文件系统: %v", err)
		}
		handler = http.FileServer(http.FS(subFS))
	}

	// Cache-Control wrapper is intentionally dev-mode-agnostic on the path
	// check but skips setting the header in dev mode entirely. This avoids
	// developers getting stuck on stale JS/CSS for an hour after editing
	// frontend files. ETag/If-Modified-Since negotiation from the inner
	// http.FileServer is preserved (we only Set an additional header).
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !devMode && r.URL.Path != "/" && r.URL.Path != "/index.html" {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		handler.ServeHTTP(w, r)
	}))
}
