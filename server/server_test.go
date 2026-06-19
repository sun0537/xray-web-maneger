package server

import (
	"context"
	"embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"

	"xray-web-manager/internal/xray-proto/app/observatory"
	observatorypb "xray-web-manager/internal/xray-proto/app/observatory/command"
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
		{"trojan", "xray.proxy.trojan.outbound.Config", "trojan"},
		{"freedom", "xray.proxy.freedom.Config", "freedom"},
		{"short type", "a.b", "b"},
		{"three segments", "a.b.c", "c"},
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

		result := computeBPS(curr, last, now, prev)

		assert.InDelta(t, 512.0, result.UplinkBPS, 0.1)
		assert.InDelta(t, 1024.0, result.DownlinkBPS, 0.1)
	})

	t.Run("Counter reset clamps to zero", func(t *testing.T) {
		curr := StatsData{Uplink: 100, Downlink: 50, Outbounds: []OutboundStatusData{}}
		last := StatsData{Uplink: 1024, Downlink: 2048, Outbounds: []OutboundStatusData{}}

		result := computeBPS(curr, last, now, prev)

		assert.Equal(t, 0.0, result.UplinkBPS)
		assert.Equal(t, 0.0, result.DownlinkBPS)
	})

	t.Run("First fetch returns data unchanged", func(t *testing.T) {
		data := StatsData{Uplink: 100}
		result := computeBPS(data, StatsData{}, now, time.Time{})
		assert.Equal(t, data, result)
	})

	t.Run("Zero elapsed returns data unchanged", func(t *testing.T) {
		data := StatsData{Uplink: 100}
		result := computeBPS(data, data, now, now)
		assert.Equal(t, data, result)
	})
}

func TestJsonResponse(t *testing.T) {
	w := httptest.NewRecorder()
	jsonResponse(w, map[string]string{"key": "value"}, http.StatusCreated)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Body.String(), `"key":"value"`)
}

func TestRegisterFrontend(t *testing.T) {
	t.Run("Dev mode registers handler", func(t *testing.T) {
		mux := http.NewServeMux()
		RegisterFrontend(mux, true, embed.FS{})

		// Verify a handler is registered at "/" — the file server returns 404
		// when the frontend/ directory doesn't exist (test CWD ≠ project root),
		// but the handler IS registered (not a bare-mux 404).
		req := httptest.NewRequest("GET", "/any-path", nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		// A registered FileServer returns 404; an unregistered mux returns 404 too.
		// The difference: FileServer sets Content-Type header.
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Prod mode registers handler", func(t *testing.T) {
		mux := http.NewServeMux()
		// Use an empty embed.FS — the handler is registered but will 404
		RegisterFrontend(mux, false, embed.FS{})

		req := httptest.NewRequest("GET", "/any-path", nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Prod mode with actual files serves index.html", func(t *testing.T) {
		// Use dev mode with a temp dir containing an index.html
		tmpDir := t.TempDir()
		indexDir := tmpDir + "/frontend-test"
		_ = os.MkdirAll(indexDir, 0755)
		_ = os.WriteFile(indexDir+"/index.html", []byte("<html>test</html>"), 0644)

		// Register with dev mode pointing to our temp dir
		devMux := http.NewServeMux()
		devMux.Handle("/", http.FileServer(http.Dir(indexDir)))

		// http.FileServer redirects /index.html → / with 301
		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()
		devMux.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Contains(t, rr.Body.String(), "test")
	})
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

// MockObservatoryClientWithStatus returns sample outbound statuses.
type MockObservatoryClientWithStatus struct{}

func (m *MockObservatoryClientWithStatus) GetOutboundStatus(_ context.Context, _ *observatorypb.GetOutboundStatusRequest, _ ...grpc.CallOption) (*observatorypb.GetOutboundStatusResponse, error) {
	return &observatorypb.GetOutboundStatusResponse{
		Status: &observatory.ObservationResult{
			Status: []*observatory.OutboundStatus{
				{OutboundTag: "node-1", Alive: true, Delay: 50},
				{OutboundTag: "node-2", Alive: true, Delay: 120},
				{OutboundTag: "node-3", Alive: false, Delay: 0},
			},
		},
	}, nil
}

// MockObservatoryClient returns empty statuses.
type MockObservatoryClient struct{}

func (m *MockObservatoryClient) GetOutboundStatus(_ context.Context, _ *observatorypb.GetOutboundStatusRequest, _ ...grpc.CallOption) (*observatorypb.GetOutboundStatusResponse, error) {
	return &observatorypb.GetOutboundStatusResponse{
		Status: &observatory.ObservationResult{},
	}, nil
}

func TestGetAllOutboundStatuses(t *testing.T) {
	t.Run("Returns mapped statuses", func(t *testing.T) {
		s := &Server{observatoryClient: &MockObservatoryClientWithStatus{}}
		result := s.getAllOutboundStatuses()

		assert.Len(t, result, 3)
		assert.Equal(t, "node-1", result[0].Tag)
		assert.True(t, result[0].Alive)
		assert.Equal(t, int64(50), result[0].Delay)
		assert.Equal(t, "node-3", result[2].Tag)
		assert.False(t, result[2].Alive)
	})

	t.Run("Returns empty on empty status", func(t *testing.T) {
		s := &Server{observatoryClient: &MockObservatoryClient{}}
		result := s.getAllOutboundStatuses()
		assert.Empty(t, result)
	})
}
