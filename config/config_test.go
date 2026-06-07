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
				Log:    LogConfig{MaxBytes: 10 * 1024 * 1024},
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
				Log:    LogConfig{MaxBytes: 10 * 1024 * 1024},
			},
			wantErr: false,
		},
		{
			name: "log.max_bytes = 0 应被拒绝",
			config: Config{
				Server: ServerConfig{Host: "localhost", Port: "8080"},
				Xray:   XrayConfig{ApiAddr: "localhost:10085", BalancerTag: "balancer"},
				Log:    LogConfig{MaxBytes: 0},
			},
			wantErr: true,
		},
		{
			name: "log.max_bytes 负值应被拒绝",
			config: Config{
				Server: ServerConfig{Host: "localhost", Port: "8080"},
				Xray:   XrayConfig{ApiAddr: "localhost:10085", BalancerTag: "balancer"},
				Log:    LogConfig{MaxBytes: -1},
			},
			wantErr: true,
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
	assert.Equal(t, "9098", cfg.Server.Port)
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
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
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

func TestLoadConfig(t *testing.T) {
	t.Run("Valid config file", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "config.yaml")
		content := `
server:
  host: "127.0.0.1"
  port: "9999"
xray:
  api_addr: "localhost:10085"
  balancer_tag: "balancer"
log:
  type: "none"
`
		os.WriteFile(path, []byte(content), 0644)

		cfg, err := LoadConfig(path)
		assert.NoError(t, err)
		assert.Equal(t, "127.0.0.1", cfg.Server.Host)
		assert.Equal(t, "9999", cfg.Server.Port)
	})

	t.Run("Missing file returns defaults", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "missing.yaml")

		cfg, err := LoadConfig(path)
		assert.NoError(t, err)
		assert.Equal(t, "0.0.0.0", cfg.Server.Host)
		assert.Equal(t, "9098", cfg.Server.Port)
		assert.Equal(t, "localhost:10085", cfg.Xray.ApiAddr)
	})

	t.Run("Invalid YAML returns error", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "bad.yaml")
		os.WriteFile(path, []byte("{invalid: [yaml"), 0644)

		_, err := LoadConfig(path)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "解析配置文件失败")
	})

	t.Run("Empty fields get defaults", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "config.yaml")
		os.WriteFile(path, []byte("server:\n  host: \"\"\nxray:\n  api_addr: \"localhost:10085\"\n  balancer_tag: \"b\"\n"), 0644)

		cfg, err := LoadConfig(path)
		assert.NoError(t, err)
		assert.Equal(t, "0.0.0.0", cfg.Server.Host)
		assert.Equal(t, "9098", cfg.Server.Port)
	})

	t.Run("Log config file type", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "config.yaml")
		content := `
server:
  port: "9098"
xray:
  api_addr: "localhost:10085"
  balancer_tag: "balancer"
log:
  type: "file"
  file_path: "/var/log/xray.log"
`
		os.WriteFile(path, []byte(content), 0644)

		cfg, err := LoadConfig(path)
		assert.NoError(t, err)
		assert.Equal(t, "file", cfg.Log.Type)
		assert.Equal(t, "/var/log/xray.log", cfg.Log.FilePath)
	})
}

func TestValidateConfigLog(t *testing.T) {
	const defaultMaxBytes = 10 * 1024 * 1024

	validBase := func() Config {
		return Config{
			Server: ServerConfig{Host: "localhost", Port: "8080"},
			Xray:   XrayConfig{ApiAddr: "localhost:10085", BalancerTag: "balancer"},
			Log:    LogConfig{MaxBytes: defaultMaxBytes},
		}
	}

	t.Run("Log type none is valid", func(t *testing.T) {
		cfg := validBase()
		cfg.Log.Type = "none"
		assert.NoError(t, ValidateConfig(cfg))
	})

	t.Run("Log type empty is valid", func(t *testing.T) {
		cfg := validBase()
		cfg.Log.Type = ""
		assert.NoError(t, ValidateConfig(cfg))
	})

	t.Run("Log type file valid", func(t *testing.T) {
		cfg := validBase()
		cfg.Log.Type = "file"
		cfg.Log.FilePath = "/var/log/xray.log"
		assert.NoError(t, ValidateConfig(cfg))
	})

	t.Run("Log type file missing path", func(t *testing.T) {
		cfg := validBase()
		cfg.Log.Type = "file"
		cfg.Log.FilePath = ""
		assert.Error(t, ValidateConfig(cfg))
	})

	t.Run("Log type journal valid", func(t *testing.T) {
		cfg := validBase()
		cfg.Log.Type = "journal"
		cfg.Log.Journal = "xray"
		assert.NoError(t, ValidateConfig(cfg))
	})

	t.Run("Log type journal missing name", func(t *testing.T) {
		cfg := validBase()
		cfg.Log.Type = "journal"
		cfg.Log.Journal = ""
		assert.Error(t, ValidateConfig(cfg))
	})

	t.Run("Log type journal invalid chars", func(t *testing.T) {
		cfg := validBase()
		cfg.Log.Type = "journal"
		cfg.Log.Journal = "xray service"
		assert.Error(t, ValidateConfig(cfg))
	})

	t.Run("Log type journal with dots and dashes", func(t *testing.T) {
		cfg := validBase()
		cfg.Log.Type = "journal"
		cfg.Log.Journal = "xray-v2.service"
		assert.NoError(t, ValidateConfig(cfg))
	})

	t.Run("Invalid log type", func(t *testing.T) {
		cfg := validBase()
		cfg.Log.Type = "syslog"
		assert.Error(t, ValidateConfig(cfg))
	})
}
