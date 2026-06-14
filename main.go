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
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"xray-web-manager/config"
	"xray-web-manager/middleware"
	"xray-web-manager/server"
	"xray-web-manager/sse"
)

//go:embed frontend/index.html frontend/core.js frontend/network.js frontend/features.js frontend/style.css
var frontendFS embed.FS

// waitForReady waits for the gRPC connection to reach Ready or
// TransientFailure, or for the timeout to expire. Returns true if Ready.
// Uses grpc.ClientConn.WaitForStateChange, which is event-driven and avoids
// the timer/ticker noise of a manual poll loop. On ctx cancel/timeout the
// current state is logged to help diagnose tight startup budgets.
func waitForReady(conn *grpc.ClientConn, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		state := conn.GetState()
		if state == connectivity.Ready {
			return true
		}
		if state == connectivity.TransientFailure {
			return false
		}
		if !conn.WaitForStateChange(ctx, state) {
			log.Printf("waitForReady: ctx 取消或超时，当前状态=%s", conn.GetState())
			return false
		}
	}
}

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

	conn, err := grpc.NewClient(cfg.Xray.ApiAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                5 * time.Minute,
			Timeout:             20 * time.Second,
			PermitWithoutStream: false,
		}),
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff:           backoff.DefaultConfig,
			MinConnectTimeout: 2 * time.Second,
		}),
	)
	if err != nil {
		log.Fatalf("无法创建 gRPC 客户端: %v", err)
	}
	defer conn.Close()

	const maxRetries = 3
	const connectTimeout = 5 * time.Second

	for i := 0; i < maxRetries; i++ {
		conn.Connect()
		if waitForReady(conn, connectTimeout) {
			break
		}
		log.Printf("连接失败 (尝试 %d/%d): 当前状态 %s", i+1, maxRetries, conn.GetState())
		if i < maxRetries-1 {
			// Exponential backoff: 1s, 2s (last attempt has no delay)
			backoff := time.Duration(1<<i) * time.Second
			time.Sleep(backoff)
		}
	}

	if conn.GetState() != connectivity.Ready {
		log.Fatalf("无法连接到 Xray gRPC API (%s)，当前状态: %s，请检查配置", cfg.Xray.ApiAddr, conn.GetState())
	}

	log.Println("已创建 gRPC 客户端连接")

	middleware.InitRateLimiter()

	sseMgr := sse.NewManager()
	srv := server.NewServer(cfg, conn, sseMgr, time.Now())
	srv.StartReconnectMonitor(conn)

	mainMux := http.NewServeMux()
	apiMux := http.NewServeMux()
	srv.RegisterHandlers(apiMux)

	originCheckMiddleware := middleware.CheckOrigin(cfg.Server.AllowedOrigins)
	mainMux.Handle("/api/", originCheckMiddleware(apiMux))

	server.RegisterFrontend(mainMux, *devMode, frontendFS)

	var finalHandler http.Handler = mainMux
	finalHandler = middleware.RateLimit(cfg.Server.TrustProxyHeaders, finalHandler)
	finalHandler = middleware.BasicAuth(cfg.Auth.Username, cfg.Auth.Password)(finalHandler)
	finalHandler = middleware.SecurityHeaders(finalHandler)
	finalHandler = middleware.Logger(cfg.Server.TrustProxyHeaders, finalHandler)
	finalHandler = middleware.Recovery(finalHandler)

	addr := net.JoinHostPort(cfg.Server.Host, cfg.Server.Port)
	// WriteTimeout is set to 30s so that slow clients cannot hold
	// connections indefinitely for non-SSE routes. The SSE handler
	// explicitly removes this deadline via http.NewResponseController
	// because SSE streams are long-lived by design.
	httpServer := &http.Server{
		Addr:         addr,
		Handler:      finalHandler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
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

		log.Println("阶段 1/4: 关闭 SSE 连接...")
		srv.Shutdown()

		log.Println("阶段 2/4: 关闭 HTTP 服务器...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := httpServer.Shutdown(ctx); err != nil {
			log.Printf("优雅关闭失败: %v，强制关闭", err)
			if closeErr := httpServer.Close(); closeErr != nil {
				log.Printf("强制关闭 HTTP 服务器失败: %v", closeErr)
			}
		}
		cancel()

		log.Println("阶段 3/4: 停止速率限制清理协程...")
		middleware.StopCleanup()

		log.Println("阶段 4/4: 优雅关闭完成")
	}
}
