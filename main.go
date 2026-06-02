package main

import (
	"context"
	"embed"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"xray-web-manager/config"
	"xray-web-manager/middleware"
	"xray-web-manager/server"
	"xray-web-manager/sse"
)

//go:embed frontend
var frontendFS embed.FS

var devMode = flag.Bool("dev", false, "开发模式：使用外部文件而不是嵌入文件")
var configPath = flag.String("config", "", "指定配置文件路径 (config.yaml)")

func main() {
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
			grpc.WithKeepaliveParams(keepalive.ClientParameters{
				Time:                30 * time.Second,
				Timeout:             10 * time.Second,
				PermitWithoutStream: true,
			}),
			grpc.WithConnectParams(grpc.ConnectParams{
				Backoff:           backoff.DefaultConfig,
				MinConnectTimeout: 2 * time.Second,
			}),
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
	srv := server.NewServer(cfg, conn, sseMgr, time.Now())

	mainMux := http.NewServeMux()
	apiMux := http.NewServeMux()
	srv.RegisterHandlers(apiMux)

	originCheckMiddleware := middleware.CheckOrigin(cfg.Server.AllowedOrigins)
	mainMux.Handle("/api/", originCheckMiddleware(apiMux))

	server.RegisterFrontend(mainMux, *devMode, frontendFS)

	var finalHandler http.Handler = mainMux
	finalHandler = middleware.SecurityHeaders(finalHandler)
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
