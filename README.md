# xray-web-manager

使用负载均衡出站 (Balancer) 进行节点切换，显示节点状态和系统占用信息

使用 Go 语言（go:embed）将所有前端文件（HTML/JS/CSS）嵌入到单一的可执行文件中

主要功能：  
- 节点切换：以卡片形式展示所有可用的出站节点，点击即可切换 routing.balancers 使用的节点。  
  
- 自动切换：页面首次加载时，切换到延迟最低的可用节点。  
- 节点状态：显示每个出站节点的协议、存活状态和延迟 (ms)。  
- 流量统计和系统状态：通过 SSE 实时显示 Xray 的总上行/总下行流量 (仅 inbound)，显示 Xray 进程的运行时间、内存占用和 Goroutine 数量。  
- 过滤非代理节点：过滤掉 direct、block、dns、freedom、loopback、blackhole 等非代理出站，只显示真正的代理节点。  
- 日志查看：支持从文件或 systemd journal 读取 Xray 日志，提供关键词搜索和自动刷新功能。

默认配置文件config.yaml：
```yaml
server:
    host: 0.0.0.0
    port: "9098"
    trust_proxy_headers: false
xray:
    api_addr: localhost:10085
    balancer_tag: balancer
# 不设置或留空 = 不启用认证（默认行为）
auth: {}

# 启用认证
auth:
  username: "admin"
  password: "your_password"

# 日志查看 (可选)
log:
  type: "journal"          # "file" | "journal" | "none"(默认不启用)
  # file_path: "/var/log/xray/access.log"  # type=file 时必填
  journal: "xray"          # type=journal 时必填, systemd 服务名 (不含 .service 后缀)
```
- server.host:server.port 监听的 IP 和 端口，默认为 0.0.0.0:9098，web 服务监听的端口
- allowed_origins: ["http://192.168.8.10:9098"] 允许访问的来源
- trust_proxy_headers: true 是否信任 X-Real-IP / X-Forwarded-For 请求头来识别客户端真实 IP。仅在反向代理 (如 Nginx/Caddy) 后方时设为 true，默认 false
- xray.api_addr 指向 Xray 的 gRPC API 地址
- xray.balancer_tag 与 Xray 配置中的 routing.balancers.tag 一致
- log.type 日志来源类型：`file` 从文件读取，`journal` 从 systemd journal 读取，`none` 或不配置则不启用日志功能
- log.file_path 日志文件路径，仅 `type: file` 时必填
- log.journal systemd 服务名称（不含 `.service` 后缀），仅 `type: journal` 时必填

启动，默认加载二进制文件所在位置的配置文件
```bash
./xray-web-manager -c config.yaml
```
访问浏览器http://<服务器IP>:端口

## 编译构建

### 依赖环境

| 依赖 | 版本要求 | 用途 | 安装方式 |
|------|----------|------|----------|
| Go | >= 1.21 | 后端编译 | https://go.dev/dl/ |
| Node.js | >= 18 | 前端 CSS 构建 | https://nodejs.org/ |
| npm | 随 Node.js 安装 | 前端依赖管理 | 随 Node.js 安装 |
| libsystemd-dev | - | journal 日志支持 (仅 Linux) | 见下方 |

安装 libsystemd-dev（Linux 编译 journal 支持必需）：
```bash
# Debian / Ubuntu
sudo apt-get install -y libsystemd-dev

# Fedora / RHEL / CentOS
sudo dnf install -y systemd-devel

# Arch Linux
sudo pacman -S systemd
```

> Windows 交叉编译不需要 libsystemd-dev，journal 功能在 Linux 以外平台自动禁用。

### 编译

> **注意**：`frontend/style.css` 是 Tailwind CSS 的编译产物，被 `.gitignore` 忽略。  
> 首次 clone 后必须先执行 `make frontend` 或 `make build` 生成该文件，否则编译会失败。

```bash
# 自动安装依赖 + 构建前端 + 编译
make build

# 仅安装依赖检查
make deps

# 构建发布包 (linux/windows amd64+386)
make package VERSION=v1.0.0

# 开发模式 (使用外部前端文件，热修改)
make run
```

编译产物位于 `bin/xray-web-manager`（开发）或 `release/`（发布包）。

### 项目结构

```
├── main.go                  # 入口：gRPC 连接、HTTP 服务器、信号处理
├── config/                  # 配置加载与验证
├── middleware/               # HTTP 中间件
│   ├── auth.go              #   BasicAuth + 认证限流
│   ├── cors.go              #   Origin/Referer 来源检查
│   ├── logger.go            #   请求日志 + ClientIP
│   ├── ratelimit.go         #   滑动窗口限流
│   ├── recovery.go          #   Panic 恢复
│   └── security.go          #   安全响应头
├── server/                  # HTTP 处理器
│   ├── handlerhealth.go     #   健康检查
│   ├── handlerlog.go        #   日志读取 (文件)
│   ├── handleroutbound.go   #   出站管理
│   ├── handlerstats.go      #   SSE 实时统计
│   ├── journal.go           #   systemd journal (Linux)
│   ├── journalstub.go       #   journal 桩 (非 Linux)
│   └── frontend.go          #   静态文件服务
├── sse/                     # SSE 广播器与连接管理
├── frontend/                # 前端资源
│   ├── core.js              #   全局状态 + UI 管理器
│   ├── features.js          #   节点卡片 + 标签页 + 日志
│   ├── network.js           #   API 调用 + SSE 连接
│   └── index.html           #   页面结构
└── internal/xray-proto/     # Xray gRPC protobuf 定义
```

## 项目截图

![我的项目截图](./assets/image.png)

依赖 Xray 的 gRPC API 和 routing.balancers 路由功能，确保 Xray config.json 中包含以下配置：  
- 添加 api 配置模块开启接口；  
  
- 添加 stats 模块开启统计；  
- 添加 policy 开启系统流量统计；  
- 添加 burstObservatory 模块开启连接观测，使用 HTTPing 的方式探测出站代理的连接状态；  
- route 模块中添加负载均衡器配置，代理出站不使用 outboundTag 使用 balancerTag 指定负载均衡器的 tag
<details>
<summary>Xray 配置示例</summary>

```json
{
    "api": {
        "tag": "api",
        "listen": "127.0.0.1:10085",
        "services": [
            "HandlerService",
            "RoutingService",
            "StatsService",
            "ObservatoryService",
            "LoggerService"
        ]
    },
    "stats": {},
    "policy": {
        "levels": {
            "0": {
                "statsUserUplink": true,
                "statsUserDownlink": true
            }
        },
        "system": {
            "statsInboundUplink": true,
            "statsInboundDownlink": true,
            "statsOutboundUplink": true,
            "statsOutboundDownlink": true
        }
    },
    "burstObservatory": {
        // 指定要探测的出站代理，前缀匹配
        "subjectSelector": [
            "proxy",
            "rea"
        ],
        "pingConfig": {}
    },
    "routing": {
        "domainStrategy": "IPOnDemand",
        "balancers": [
            {
                "tag": "balancer",
                // 指定使用负载的的出站代理，可以只设置一个，使用web端切换，前缀匹配
                "selector": [
                    "realityUpDown"
                ],
                "strategy": {
                    "type": "roundRobin"
                }
            }
        ],
        "rules": [
            {
                "type": "field",
                "outboundTag": "block",
                "domain": [
                    "geosite:category-ads-all"
                ]
            },
            {
                "type": "field",
                "outboundTag": "direct",
                "domain": [
                    "geosite:cn",
                    "geosite:geolocation-cn"
                ]
            },
            {
                "type": "field",
                "outboundTag": "direct",
                "ip": [
                    "223.5.5.5/32",
                    "119.29.29.29/32",
                    "180.76.76.76/32",
                    "114.114.114.114/32",
                    "geoip:cn",
                    "geoip:private"
                ]
            },
            {
                "type": "field",
                "ip": [
                    "geoip:!cn"
                ],
                "balancerTag": "balancer"
            },
            {
                "type": "field",
                "port": "0-65535",
                "balancerTag": "balancer"
            }
        ]
    }
}
```
</details>
