# xray-web-maneger

使用负载均衡出站 (Balancer) 进行节点切换，显示节点状态和系统占用信息

使用 Go 语言（go:embed）将所有前端文件（HTML/JS/CSS）嵌入到单一的可执行文件中

主要功能：  
- 节点切换：以卡片形式展示所有可用的出站节点，点击即可切换 routing.balancers 使用的节点。  
  
- 自动切换：页面首次加载时，切换到延迟最低的可用节点。  
- 节点状态：显示每个出站节点的协议、存活状态和延迟 (ms)。  
- 流量统计和系统状态：通过 SSE 实时显示 Xray 的总上行/总下行流量 (仅 inbound)，显示 Xray 进程的运行时间、内存占用和 Goroutine 数量。  
- 过滤非代理节点：过滤掉 direct、block、dns、freedom、loopback、blackhole 等非代理出站，只显示真正的代理节点。  

默认配置文件config.yaml：
```yaml
server:
    host: 0.0.0.0
    port: "9098"
xray:
    api_addr: localhost:10085
    balancer_tag: balancer
```
- server.host:server.port 监听的 IP 和 端口，默认为 0.0.0.0:9098，web 服务监听的端口
- allowed_origins: ["http://192.168.8.10:9098"] 允许访问的来源
- xray.api_addr 指向 Xray 的 gRPC API 地址
- xray.balancer_tag 与 Xray 配置中的 routing.balancers.tag 一致

启动，默认加载二进制文件所在位置的配置文件
```bash
./xray-web-manager -c config.yaml
```
访问浏览器http://<服务器IP>:端口

Xray 配置要求，修改xray的配置文件：  
添加 api 配置模块开启接口；  
添加 stats 模块开启统计；  
添加 policy 开启系统流量统计；  
添加 burstObservatory 模块开启连接观测，使用 HTTPing 的方式探测出站代理的连接状态；  
route 模块中添加负载均衡器配置，代理出站不使用 outboundTag 使用 balancerTag 指定负载均衡器的 tag
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

