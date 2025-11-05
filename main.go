package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"log"
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

// 嵌入前端文件（路径相对于 main.go 所在目录）
//
//go:embed frontend
var frontendFS embed.FS

// 开发模式标志
var devMode = flag.Bool("dev", false, "开发模式：使用外部文件而不是嵌入文件")

// (添加 -c 和 --config 标志定义)
var configPath = flag.String("c", "", "指定配置文件路径 (config.yaml)")

func main() {
	// (为 --config 提供一个别名，使其与 -c 共享同一个变量)
	flag.StringVar(configPath, "config", *configPath, "指定配置文件路径 (config.yaml)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Xray Web Manager: 一个用于切换 Xray 出站节点的 Web 管理面板。\n\n")
		fmt.Fprintf(os.Stderr, "用法 (Usage):\n")
		fmt.Fprintf(os.Stderr, "  xray-web-manager [flags]\n")
		fmt.Fprintf(os.Stderr, "可用标志 (Flags):\n")

		// --- 手动打印合并后的帮助信息 ---
		fmt.Fprintf(os.Stderr, "  -h, --help\n")
		fmt.Fprintf(os.Stderr, "        显示帮助信息\n")
		fmt.Fprintf(os.Stderr, "  -c, --config string\n")
		fmt.Fprintf(os.Stderr, "        指定配置文件路径 (config.yaml)\n")

		// --- 手动打印 -dev 标志 ---
		fmt.Fprintf(os.Stderr, "  -dev\n")
		fmt.Fprintf(os.Stderr, "        开发模式：使用外部文件而不是嵌入文件\n")
		// --- 打印结束 ---

		fmt.Fprintf(os.Stderr, "\n默认行为:\n")
		fmt.Fprintf(os.Stderr, "  如果不带任何标志运行，程序将自动查找并使用可执行文件目录下的 'config.yaml' 文件。\n")
	}
	// 解析命令行参数
	flag.Parse()

	var finalConfigPath, err = config.GetConfigPath(*configPath)
	if err != nil {
		log.Fatalf("获取配置文件路径失败: %v", err)
	}

	// 1. 加载配置文件
	config, err := config.LoadConfig(finalConfigPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 2. 连接到 Xray gRPC
	log.Printf("正在连接到 Xray gRPC API: %s", config.Xray.ApiAddr)

	var conn *grpc.ClientConn
	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		conn, err = grpc.NewClient(config.Xray.ApiAddr,
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

	// --- 3. 初始化所有组件 ---
	sseMgr := sse.NewManager()
	srv := server.NewServer(config, conn, sseMgr)

	// --- 4. 注册路由 (*** 已修改 ***) ---

	// A. 创建主路由器
	mainMux := http.NewServeMux()

	// B. 创建一个专门用于 API 的路由器，对 *这个* 路由器应用 Origin 检查
	apiMux := http.NewServeMux()
	// 将 API 处理程序注册到 API 路由器
	srv.RegisterHandlers(apiMux)

	// C. 创建受 Origin 保护的 API 处理器，从配置中获取 allowedOrigins
	originCheckMiddleware := middleware.CheckOrigin(config.Server.AllowedOrigins)
	//    只将 /api/ (以及其所有子路径) 包装在 Origin 检查中
	mainMux.Handle("/api/", originCheckMiddleware(apiMux))

	// D. 注册前端 ( *不* 应该被 Origin 检查)
	server.RegisterFrontend(mainMux, *devMode, frontendFS)

	// E.  将 *所有* 路由包装在 Logger 和 Recovery 中
	var finalHandler http.Handler = mainMux
	finalHandler = middleware.Logger(finalHandler)
	finalHandler = middleware.Recovery(finalHandler)

	// 5. 启动 Web 服务器  ===== 优雅关闭逻辑 =====
	addr := config.Server.Host + ":" + config.Server.Port
	httpServer := &http.Server{
		Addr:        addr,
		Handler:     finalHandler,
		ReadTimeout: 10 * time.Second,
		// WriteTimeout: 10 * time.Second,
		IdleTimeout: 120 * time.Second,
	}

	// 在 goroutine 中启动服务器
	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("Web 管理界面已在 http://%s 启动", addr)
		log.Printf("使用负载均衡器: %s", config.Xray.BalancerTag)
		serverErrors <- httpServer.ListenAndServe()
	}()

	// 等待中断信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-serverErrors:
		log.Fatalf("启动服务器失败: %v", err)

	case sig := <-sigChan:
		log.Printf("收到信号: %v，开始优雅关闭...", sig)

		// 1. 先关闭所有 SSE 连接
		log.Printf("步骤 1/2: 关闭 SSE 连接...")
		srv.Shutdown()

		// 等待一小段时间让 SSE 连接完全关闭
		time.Sleep(500 * time.Millisecond)

		// 2. 然后关闭 HTTP 服务器
		log.Printf("步骤 2/2: 关闭 HTTP 服务器...")

		// 使用较短的超时，因为 SSE 连接已经关闭
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()

		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("服务器关闭出现问题: %v", err)
			// 强制关闭
			if closeErr := httpServer.Close(); closeErr != nil {
				log.Printf("强制关闭失败: %v", closeErr)
			}
		} else {
			log.Println("服务器已优雅关闭")
		}
	}
}
