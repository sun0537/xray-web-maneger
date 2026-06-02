package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateConfig(t *testing.T) {
	// "表驱动测试"
	testCases := []struct {
		name    string // 测试用例名称
		config  Config // 输入的配置
		wantErr bool   // 我们是否期望它返回错误
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
	}

	// 遍历所有测试用例
	for _, tc := range testCases {
		// t.Run 允许在 'go test -v' 中看到每一个用例的名称
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConfig(tc.config)

			if tc.wantErr {
				//  *期望* 出现错误
				assert.Error(t, err)
			} else {
				// *不期望* 出现错误
				assert.NoError(t, err)
			}
		})
	}
}
