package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"xray-web-manager/config"
	"xray-web-manager/middleware"
	"xray-web-manager/server"
	"xray-web-manager/sse"
)

//go:embed frontend
var frontendFS embed.FS

var devMode = flag.Bool("dev", false, "开发模式：使用外部文件而不是嵌入文件")
var configPath = flag.String("c,config", "", "指定配置文件路径 (config.yaml)")

var startTime time.Time

func main() {
	startTime = time.Now()
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Xray Web Manager: 一个用于切换 Xray 出站节点的 Web 管理面板。\n\n")
		fmt.Fprintf(os.Stderr, "用法 (Usage):\n")
		fmt.Fprintf(os.Stderr, "  xray-web-manager [flags]\n")
		fmt.Fprintf(os.Stderr, "可用标志 (Flags):\n")
		fmt.Fprintf(os.Stderr, "  -h, --help\n")
		fmt.Fprintf(os.Stderr, "        显示帮助信息\n")
		fmt.Fprintf(os.Stderr, "  -c, --config string\n")
		fmt.Fprintf(os.Stderr, "        指定配置文件路径 (config.yaml)\n")
		fmt.Fprintf(os.Stderr, "  -dev\n")
		fmt.Fprintf(os.Stderr, "        开发模式：使用外部文件而不是嵌入文件\n")
		fmt.Fprintf(os.Stderr, "\n默认行为:\n")
		fmt.Fprintf(os.Stderr, "  如果不带任何标志运行，程序将自动查找并使用可执行文件目录下的 'config.yaml' 文件。\n")
	}
	flag.Parse()

	finalConfigPath, err := config.GetConfigPath(*configPath)
	if err != nil {
		log.Fatalf("获取配置文件路径失败: %v", err)
	}

	cfg, err := config.LoadConfig(finalConfigPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	log.Printf("正在连接到 Xray gRPC API: %s", cfg.Xray.ApiAddr)

	var conn *grpc.ClientConn
	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		conn, err = grpc.NewClient(cfg.Xray.ApiAddr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err == nil {
			break
		}
		log.Printf("连接失败 (尝试 %d/%d): %v", i+1, maxRetries, err)
		time.Sleep(time.Second * 2)
	}

	if err != nil {
		log.Fatalf("无法连接到 Xray gRPC (已尝试 %d 次): %v", maxRetries, err)
	}
	defer conn.Close()

	log.Println("已成功连接到 Xray gRPC API")

	sseMgr := sse.NewManager()
	srv := server.NewServer(cfg, conn, sseMgr, startTime)

	mainMux := http.NewServeMux()
	apiMux := http.NewServeMux()
	srv.RegisterHandlers(apiMux)

	originCheckMiddleware := middleware.CheckOrigin(cfg.Server.AllowedOrigins)
	mainMux.Handle("/api/", originCheckMiddleware(apiMux))

	server.RegisterFrontend(mainMux, *devMode, frontendFS)

	var finalHandler http.Handler = mainMux
	finalHandler = middleware.BasicAuth(cfg.Auth.Username, cfg.Auth.Password)(finalHandler)
	finalHandler = middleware.Logger(finalHandler)
	finalHandler = middleware.Recovery(finalHandler)

	addr := net.JoinHostPort(cfg.Server.Host, cfg.Server.Port)
	httpServer := &http.Server{
		Addr:        addr,
		Handler:     finalHandler,
		ReadTimeout: 10 * time.Second,
		IdleTimeout: 120 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("Web 管理界面已在 http://%s 启动", addr)
		log.Printf("使用负载均衡器: %s", cfg.Xray.BalancerTag)
		serverErrors <- httpServer.ListenAndServe()
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-serverErrors:
		log.Fatalf("启动服务器失败: %v", err)

	case sig := <-sigChan:
		log.Printf("收到终止信号: %v，开始优雅关闭...", sig)

		log.Println("阶段 1/3: 关闭 SSE 连接...")
		srv.Shutdown()

		log.Println("阶段 2/3: 关闭 HTTP 服务器...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := httpServer.Shutdown(ctx); err != nil {
			log.Printf("优雅关闭失败: %v，强制关闭", err)
			httpServer.Close()
		}
		cancel()

		log.Println("阶段 3/3: 优雅关闭完成")
	}
}
