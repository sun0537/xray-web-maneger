package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateConfig(t *testing.T) {
	testCases := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name: "有效配置",
			config: Config{
				Server: ServerConfig{Host: "localhost", Port: "8080"},
				Xray:   XrayConfig{ApiAddr: "localhost:10085", BalancerTag: "balancer"},
			},
			wantErr: false,
		},
		{
			name: "无效端口 (太高)",
			config: Config{
				Server: ServerConfig{Host: "localhost", Port: "99999"},
				Xray:   XrayConfig{ApiAddr: "localhost:10085", BalancerTag: "balancer"},
			},
			wantErr: true,
		},
		{
			name: "无效端口 (非数字)",
			config: Config{
				Server: ServerConfig{Host: "localhost", Port: "http"},
				Xray:   XrayConfig{ApiAddr: "localhost:10085", BalancerTag: "balancer"},
			},
			wantErr: true,
		},
		{
			name: "缺少 API 地址",
			config: Config{
				Server: ServerConfig{Host: "localhost", Port: "8080"},
				Xray:   XrayConfig{ApiAddr: "", BalancerTag: "balancer"},
			},
			wantErr: true,
		},
		{
			name: "空负载均衡标签",
			config: Config{
				Server: ServerConfig{Host: "localhost", Port: "8080"},
				Xray:   XrayConfig{ApiAddr: "localhost:10085", BalancerTag: ""},
			},
			wantErr: true,
		},
		{
			name: "API 地址格式错误 (无冒号)",
			config: Config{
				Server: ServerConfig{Host: "localhost", Port: "8080"},
				Xray:   XrayConfig{ApiAddr: "localhost10085", BalancerTag: "balancer"},
			},
			wantErr: true,
		},
		{
			name: "API 地址缺少主机",
			config: Config{
				Server: ServerConfig{Host: "localhost", Port: "8080"},
				Xray:   XrayConfig{ApiAddr: ":10085", BalancerTag: "balancer"},
			},
			wantErr: true,
		},
		{
			name: "API 地址端口无效",
			config: Config{
				Server: ServerConfig{Host: "localhost", Port: "8080"},
				Xray:   XrayConfig{ApiAddr: "localhost:abc", BalancerTag: "balancer"},
			},
			wantErr: true,
		},
		{
			name: "非法 Origin",
			config: Config{
				Server: ServerConfig{Host: "localhost", Port: "8080", AllowedOrigins: []string{"not-a-url"}},
				Xray:   XrayConfig{ApiAddr: "localhost:10085", BalancerTag: "balancer"},
			},
			wantErr: true,
		},
		{
			name: "合法 Origin",
			config: Config{
				Server: ServerConfig{Host: "localhost", Port: "8080", AllowedOrigins: []string{"https://example.com"}},
				Xray:   XrayConfig{ApiAddr: "localhost:10085", BalancerTag: "balancer"},
			},
			wantErr: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConfig(tc.config)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestNewDefaultConfig(t *testing.T) {
	cfg := newDefaultConfig()
	assert.Equal(t, "0.0.0.0", cfg.Server.Host)
	assert.Equal(t, "8080", cfg.Server.Port)
	assert.Equal(t, "localhost:10085", cfg.Xray.ApiAddr)
	assert.Equal(t, "balancer", cfg.Xray.BalancerTag)
}

func TestSaveDefaultConfig(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "subdir", "config.yaml")

	err := SaveDefaultConfig(path)
	assert.NoError(t, err)

	data, err := os.ReadFile(path)
	assert.NoError(t, err)
	assert.Contains(t, string(data), "Xray Web Manager")

	info, err := os.Stat(path)
	assert.NoError(t, err)
	assert.Equal(t, os.FileMode(0644), info.Mode().Perm())
}

func TestApplyDefault(t *testing.T) {
	empty := ""
	applyDefault(&empty, "fallback")
	assert.Equal(t, "fallback", empty)

	set := "already-set"
	applyDefault(&set, "fallback")
	assert.Equal(t, "already-set", set)
}

func TestGetConfigPath(t *testing.T) {
	t.Run("Explicit path returned as-is", func(t *testing.T) {
		path, err := GetConfigPath("/tmp/custom.yaml")
		assert.NoError(t, err)
		assert.Equal(t, "/tmp/custom.yaml", path)
	})

	t.Run("Empty path uses executable dir", func(t *testing.T) {
		path, err := GetConfigPath("")
		assert.NoError(t, err)
		assert.Contains(t, path, "config.yaml")
	})
}

