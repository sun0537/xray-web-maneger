package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"xray-web-manager/internal/xray-proto/common/serial"
	"xray-web-manager/middleware"
)

func TestParseProtocol(t *testing.T) {
	tests := []struct {
		name     string
		typeName string
		want     string
	}{
		{"vmess outbound", "xray.proxy.vmess.outbound.Config", "vmess"},
		{"vless outbound", "xray.proxy.vless.outbound.Config", "vless"},
		{"shadowsocks", "xray.proxy.shadowsocks.outbound.Config", "shadowsocks"},
		{"freedom", "xray.proxy.freedom.Config", "freedom"},
		{"short type", "a.b", "b"},
		{"single part", "single", "single"},
		{"empty type", "", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var settings *serial.TypedMessage
			if tt.typeName != "" {
				settings = &serial.TypedMessage{Type: tt.typeName}
			}
			got := parseProtocol(settings)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsValidOutbound(t *testing.T) {
	assert.True(t, isValidOutbound("vmess"))
	assert.True(t, isValidOutbound("vless"))
	assert.True(t, isValidOutbound("trojan"))
	assert.True(t, isValidOutbound("wireguard"))
	assert.False(t, isValidOutbound("freedom"))
	assert.False(t, isValidOutbound("blackhole"))
	assert.False(t, isValidOutbound("dns"))
	assert.False(t, isValidOutbound("loopback"))
}

func TestComputeBPS(t *testing.T) {
	now := time.Now()
	prev := now.Add(-2 * time.Second)

	t.Run("Normal positive BPS", func(t *testing.T) {
		curr := StatsData{Uplink: 2048, Downlink: 4096, Outbounds: []OutboundStatusData{}}
		last := StatsData{Uplink: 1024, Downlink: 2048, Outbounds: []OutboundStatusData{}}
		currJSON, _ := json.Marshal(curr)
		lastJSON, _ := json.Marshal(last)

		result := computeBPS(currJSON, lastJSON, now, prev)

		var enriched StatsData
		json.Unmarshal(result, &enriched)
		assert.InDelta(t, 512.0, enriched.UplinkBPS, 0.1)
		assert.InDelta(t, 1024.0, enriched.DownlinkBPS, 0.1)
	})

	t.Run("Counter reset clamps to zero", func(t *testing.T) {
		curr := StatsData{Uplink: 100, Downlink: 50, Outbounds: []OutboundStatusData{}}
		last := StatsData{Uplink: 1024, Downlink: 2048, Outbounds: []OutboundStatusData{}}
		currJSON, _ := json.Marshal(curr)
		lastJSON, _ := json.Marshal(last)

		result := computeBPS(currJSON, lastJSON, now, prev)

		var enriched StatsData
		json.Unmarshal(result, &enriched)
		assert.Equal(t, 0.0, enriched.UplinkBPS)
		assert.Equal(t, 0.0, enriched.DownlinkBPS)
	})

	t.Run("First fetch returns data unchanged", func(t *testing.T) {
		data := []byte(`{"uplink":100}`)
		result := computeBPS(data, nil, now, time.Time{})
		assert.Equal(t, data, result)
	})

	t.Run("Zero elapsed returns data unchanged", func(t *testing.T) {
		data := []byte(`{"uplink":100}`)
		result := computeBPS(data, data, now, now)
		assert.Equal(t, data, result)
	})

	t.Run("Invalid JSON returns data unchanged", func(t *testing.T) {
		result := computeBPS([]byte("bad"), []byte("bad"), now, prev)
		assert.Equal(t, []byte("bad"), result)
	})
}

func TestJsonResponse(t *testing.T) {
	w := httptest.NewRecorder()
	jsonResponse(w, map[string]string{"key": "value"}, http.StatusCreated)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Body.String(), `"key":"value"`)
}

func TestJsonError(t *testing.T) {
	w := httptest.NewRecorder()
	jsonError(w, "test error", http.StatusBadRequest, "validation")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp middleware.ErrorResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "test error", resp.Error)
	assert.Equal(t, "validation", resp.ErrorType)
}

func TestGetAllOutboundStatuses(t *testing.T) {
	t.Run("Returns mapped statuses", func(t *testing.T) {
		s := &Server{observatoryClient: &MockObservatoryClientWithStatus{}}
		result := s.getAllOutboundStatuses(context.Background())

		assert.Len(t, result, 3)
		assert.Equal(t, "node-1", result[0].Tag)
		assert.True(t, result[0].Alive)
		assert.Equal(t, int64(50), result[0].Delay)
		assert.Equal(t, "node-3", result[2].Tag)
		assert.False(t, result[2].Alive)
	})

	t.Run("Returns empty on empty status", func(t *testing.T) {
		s := &Server{observatoryClient: &MockObservatoryClient{}}
		result := s.getAllOutboundStatuses(context.Background())
		assert.Empty(t, result)
	})
}
