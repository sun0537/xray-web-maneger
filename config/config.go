package config

import (
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

// ServerConfig holds the HTTP server configuration.
type ServerConfig struct {
	Host                string   `yaml:"host"`                  // 监听地址
	Port                string   `yaml:"port"`                  // 监听端口
	AllowedOrigins      []string `yaml:"allowed_origins"`       // (可选) 允许访问的来源 (用于安全检查)
	TrustProxyHeaders   bool     `yaml:"trust_proxy_headers"`   // 是否信任 X-Real-IP / X-Forwarded-For 头 (仅在反向代理后设为 true)
}

// XrayConfig holds the Xray gRPC API connection configuration.
type XrayConfig struct {
	ApiAddr     string `yaml:"api_addr"`     // Xray gRPC 地址
	BalancerTag string `yaml:"balancer_tag"` // 负载均衡器标签
}

// AuthConfig holds the HTTP Basic Auth credentials.
type AuthConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// Config 配置文件结构
type Config struct {
	Server ServerConfig `yaml:"server"`
	Xray   XrayConfig   `yaml:"xray"`
	Auth   AuthConfig   `yaml:"auth"`
}

// newDefaultConfig returns an immutable copy of the default configuration.
func newDefaultConfig() Config {
	return Config{
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: "8080",
		},
		Xray: XrayConfig{
			ApiAddr:     "localhost:10085",
			BalancerTag: "balancer",
		},
	}
}

func GetConfigPath(configPath string) (string, error) {
	if configPath != "" {
		cleaned := filepath.Clean(configPath)
		if !filepath.IsAbs(cleaned) {
			absPath, err := filepath.Abs(cleaned)
			if err != nil {
				return "", fmt.Errorf("无法解析配置文件路径: %w", err)
			}
			cleaned = absPath
		}
		ext := filepath.Ext(cleaned)
		if ext != ".yaml" && ext != ".yml" {
			return "", fmt.Errorf("配置文件必须是 .yaml 或 .yml 格式，得到: %s", ext)
		}
		log.Printf("信息: 正在使用标志指定的配置文件: %s", cleaned)
		return cleaned, nil
	}

	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("无法获取可执行文件路径: %w", err)
	}
	exeDir := filepath.Dir(exePath)
	finalPath := filepath.Join(exeDir, "config.yaml")
	log.Printf("信息: 未指定配置，使用默认路径: %s", finalPath)
	return finalPath, nil
}

func LoadConfig(filename string) (Config, error) {
	defaultCfg := newDefaultConfig()
	config := defaultCfg

	data, err := os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("配置文件 %s 不存在，使用默认配置", filename)
			if err := SaveDefaultConfig(filename); err != nil {
				log.Printf("警告: 无法创建默认配置文件: %v", err)
			}
			return config, nil
		}
		return config, fmt.Errorf("读取配置文件失败: %w", err)
	}

	if err := yaml.Unmarshal(data, &config); err != nil {
		return newDefaultConfig(), fmt.Errorf("解析配置文件失败: %w", err)
	}

	applyDefault(&config.Server.Host, defaultCfg.Server.Host)
	applyDefault(&config.Server.Port, defaultCfg.Server.Port)
	applyDefault(&config.Xray.ApiAddr, defaultCfg.Xray.ApiAddr)
	applyDefault(&config.Xray.BalancerTag, defaultCfg.Xray.BalancerTag)

	if err := ValidateConfig(config); err != nil {
		return newDefaultConfig(), fmt.Errorf("配置验证失败: %w", err)
	}

	log.Printf("配置已加载: %s:%s", config.Server.Host, config.Server.Port)
	if len(config.Server.AllowedOrigins) > 0 {
		log.Printf("安全: 已启用 Origin 检查, 允许的来源: %v", config.Server.AllowedOrigins)
	} else {
		log.Println("安全: 未配置 allowed_origins, 将跳过 Origin 检查")
	}
	return config, nil
}

func applyDefault(field *string, defaultVal string) {
	if *field == "" {
		*field = defaultVal
	}
}

func ValidateConfig(config Config) error {
	port, err := strconv.Atoi(config.Server.Port)
	if err != nil {
		return fmt.Errorf("无效的端口号: %s", config.Server.Port)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("端口号超出范围 (1-65535): %d", port)
	}

	if config.Xray.ApiAddr == "" {
		return fmt.Errorf("xray API 地址不能为空")
	}

	if config.Xray.BalancerTag == "" {
		return fmt.Errorf("负载均衡器标签不能为空")
	}

	apiHost, apiPort, apiErr := net.SplitHostPort(config.Xray.ApiAddr)
	if apiErr != nil {
		return fmt.Errorf("无效的 API 地址格式 (需要 host:port): %w", apiErr)
	}
	if apiHost == "" {
		return fmt.Errorf("API 地址的主机名不能为空 (如 localhost:10085)")
	}
	if apiPort == "" {
		return fmt.Errorf("API 地址的端口不能为空 (如 localhost:10085)")
	}
	if p, err := strconv.Atoi(apiPort); err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("API 地址的端口号无效 (1-65535): %s", apiPort)
	}

	for _, origin := range config.Server.AllowedOrigins {
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("无效的 Origin (需要 http(s)://host): %s", origin)
		}
	}

	return nil
}

func SaveDefaultConfig(filename string) error {
	data, err := yaml.Marshal(newDefaultConfig())
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		return err
	}

	header := []byte("# Xray Web Manager 配置文件\n\n")
	return os.WriteFile(filename, append(header, data...), 0644)
}
