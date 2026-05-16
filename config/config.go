package config

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config 配置文件结构
type Config struct {
	Server struct {
		Host           string   `yaml:"host"`            // 监听地址
		Port           string   `yaml:"port"`            // 监听端口
		AllowedOrigins []string `yaml:"allowed_origins"` // (可选) 允许访问的来源 (用于安全检查)
	} `yaml:"server"`
	Xray struct {
		ApiAddr     string `yaml:"api_addr"`     // Xray gRPC 地址
		BalancerTag string `yaml:"balancer_tag"` // 负载均衡器标签
	} `yaml:"xray"`
}

// 默认配置
var defaultConfig = Config{
	Server: struct {
		Host           string   `yaml:"host"`
		Port           string   `yaml:"port"`
		AllowedOrigins []string `yaml:"allowed_origins"`
	}{
		Host:           "0.0.0.0",
		Port:           "8080",
		AllowedOrigins: []string{},
	},
	Xray: struct {
		ApiAddr     string `yaml:"api_addr"`
		BalancerTag string `yaml:"balancer_tag"`
	}{
		ApiAddr:     "localhost:10085",
		BalancerTag: "balancer",
	},
}

func GetConfigPath(configPath string) (string, error) {
	if configPath != "" {
		log.Printf("信息: 正在使用标志指定的配置文件: %s", configPath)
		return configPath, nil
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
	config := defaultConfig

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
		return defaultConfig, fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 填充默认值
	applyDefault(&config.Server.Host, defaultConfig.Server.Host)
	applyDefault(&config.Server.Port, defaultConfig.Server.Port)
	applyDefault(&config.Xray.ApiAddr, defaultConfig.Xray.ApiAddr)
	applyDefault(&config.Xray.BalancerTag, defaultConfig.Xray.BalancerTag)

	// 验证配置
	if err := ValidateConfig(config); err != nil {
		return defaultConfig, fmt.Errorf("配置验证失败: %w", err)
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

// ValidateConfig 验证配置合法性
func ValidateConfig(config Config) error {
	// 验证端口号
	port, err := strconv.Atoi(config.Server.Port)
	if err != nil {
		return fmt.Errorf("无效的端口号: %s", config.Server.Port)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("端口号超出范围 (1-65535): %d", port)
	}

	// 验证 API 地址格式
	if config.Xray.ApiAddr == "" {
		return fmt.Errorf("xray API 地址不能为空")
	}

	// 验证负载均衡器标签
	if config.Xray.BalancerTag == "" {
		return fmt.Errorf("负载均衡器标签不能为空")
	}

	// 添加对 ApiAddr 格式的验证
	if !strings.Contains(config.Xray.ApiAddr, ":") {
		return fmt.Errorf("无效的 API 地址格式，应为 host:port")
	}

	// 验证 AllowedOrigins 格式
	for _, origin := range config.Server.AllowedOrigins {
		if _, err := url.Parse(origin); err != nil {
			return fmt.Errorf("无效的 Origin: %s", origin)
		}
	}

	return nil
}

// SaveDefaultConfig 保存默认配置文件
func SaveDefaultConfig(filename string) error {
	data, err := yaml.Marshal(defaultConfig)
	if err != nil {
		return err
	}

	header := []byte("# Xray Web Manager 配置文件\n\n")
	return os.WriteFile(filename, append(header, data...), 0644)
}
