package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"

	"xray-web-manager/config"
)

func writeTestLog(t *testing.T, lines []string) string {
	t.Helper()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.log")
	content := strings.Join(lines, "\n") + "\n"
	os.WriteFile(path, []byte(content), 0644)
	return path
}

func TestReadLogFile(t *testing.T) {
	t.Run("Read all lines", func(t *testing.T) {
		path := writeTestLog(t, []string{"line1", "line2", "line3"})

		lines, err := readLogFile(context.Background(), path, 100, "")
		assert.NoError(t, err)
		assert.Equal(t, []string{"line1", "line2", "line3"}, lines)
	})

	t.Run("Respects maxLines ring buffer", func(t *testing.T) {
		path := writeTestLog(t, []string{"a", "b", "c", "d", "e"})

		lines, err := readLogFile(context.Background(), path, 3, "")
		assert.NoError(t, err)
		assert.Equal(t, []string{"c", "d", "e"}, lines)
	})

	t.Run("Ring buffer exact capacity", func(t *testing.T) {
		path := writeTestLog(t, []string{"1", "2", "3"})

		lines, err := readLogFile(context.Background(), path, 3, "")
		assert.NoError(t, err)
		assert.Equal(t, []string{"1", "2", "3"}, lines)
	})

	t.Run("Ring buffer with more than double capacity", func(t *testing.T) {
		all := make([]string, 20)
		for i := range all {
			all[i] = "line" + string(rune('a'+i))
		}
		path := writeTestLog(t, all)

		lines, err := readLogFile(context.Background(), path, 5, "")
		assert.NoError(t, err)
		assert.Len(t, lines, 5)
		assert.Equal(t, "linep", lines[0])
		assert.Equal(t, "linet", lines[4])
	})

	t.Run("Search filter", func(t *testing.T) {
		path := writeTestLog(t, []string{"hello world", "foo bar", "hello again"})

		lines, err := readLogFile(context.Background(), path, 100, "hello")
		assert.NoError(t, err)
		assert.Equal(t, []string{"hello world", "hello again"}, lines)
	})

	t.Run("Search is case-insensitive", func(t *testing.T) {
		path := writeTestLog(t, []string{"ERROR: something", "info: ok", "error: another"})

		lines, err := readLogFile(context.Background(), path, 100, "error")
		assert.NoError(t, err)
		assert.Equal(t, []string{"ERROR: something", "error: another"}, lines)
	})

	t.Run("Search with maxLines ring buffer", func(t *testing.T) {
		path := writeTestLog(t, []string{"match1", "skip", "match2", "skip", "match3"})

		lines, err := readLogFile(context.Background(), path, 2, "match")
		assert.NoError(t, err)
		assert.Equal(t, []string{"match2", "match3"}, lines)
	})

	t.Run("Empty file", func(t *testing.T) {
		path := writeTestLog(t, []string{""})

		lines, err := readLogFile(context.Background(), path, 100, "")
		assert.NoError(t, err)
		assert.Equal(t, []string{""}, lines)
	})

	t.Run("File not found", func(t *testing.T) {
		_, err := readLogFile(context.Background(), "/nonexistent/path.log", 100, "")
		assert.Error(t, err)
	})

	t.Run("Cancelled context", func(t *testing.T) {
		path := writeTestLog(t, []string{"line1", "line2", "line3"})

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := readLogFile(ctx, path, 100, "")
		assert.Error(t, err)
	})
}

func TestReadLogFileMaxFileSize(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "big.log")

	f, _ := os.Create(path)
	chunk := strings.Repeat("x", 1024)
	for i := 0; i < (logMaxFileSize/1024)+1; i++ {
		f.WriteString(chunk + "\n")
	}
	f.Close()

	_, err := readLogFile(context.Background(), path, 100, "")
	assert.Error(t, err)

	var ftl *fileTooLargeError
	if assert.ErrorAs(t, err, &ftl) {
		msg := ftl.Error()
		assert.Contains(t, msg, "日志文件过大")
		assert.Contains(t, msg, "最大支持")
	}
}

func TestHandleGetLogs(t *testing.T) {
	t.Run("Log type none returns empty", func(t *testing.T) {
		s := &Server{
			config: config.Config{Log: config.LogConfig{Type: "none"}},
		}

		req := httptest.NewRequest("GET", "/api/logs", nil)
		rr := httptest.NewRecorder()
		s.handleGetLogs(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		var resp logResponse
		json.Unmarshal(rr.Body.Bytes(), &resp)
		assert.Equal(t, "none", resp.Source)
		assert.Empty(t, resp.Lines)
	})

	t.Run("Log type empty returns empty", func(t *testing.T) {
		s := &Server{
			config: config.Config{Log: config.LogConfig{Type: ""}},
		}

		req := httptest.NewRequest("GET", "/api/logs", nil)
		rr := httptest.NewRecorder()
		s.handleGetLogs(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("Log type file success", func(t *testing.T) {
		logPath := writeTestLog(t, []string{"2024-01-01 start", "2024-01-01 info ok", "2024-01-01 end"})

		s := &Server{
			config: config.Config{Log: config.LogConfig{Type: "file", FilePath: logPath}},
		}

		req := httptest.NewRequest("GET", "/api/logs", nil)
		rr := httptest.NewRecorder()
		s.handleGetLogs(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		var resp logResponse
		json.Unmarshal(rr.Body.Bytes(), &resp)
		assert.Equal(t, "file", resp.Source)
		assert.Len(t, resp.Lines, 3)
	})

	t.Run("Lines parameter respected", func(t *testing.T) {
		logPath := writeTestLog(t, []string{"a", "b", "c", "d", "e"})

		s := &Server{
			config: config.Config{Log: config.LogConfig{Type: "file", FilePath: logPath}},
		}

		req := httptest.NewRequest("GET", "/api/logs?lines=3", nil)
		rr := httptest.NewRecorder()
		s.handleGetLogs(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		var resp logResponse
		json.Unmarshal(rr.Body.Bytes(), &resp)
		assert.Len(t, resp.Lines, 3)
	})

	t.Run("Lines parameter capped at max", func(t *testing.T) {
		logPath := writeTestLog(t, []string{"a", "b"})

		s := &Server{
			config: config.Config{Log: config.LogConfig{Type: "file", FilePath: logPath}},
		}

		req := httptest.NewRequest("GET", "/api/logs?lines=99999", nil)
		rr := httptest.NewRecorder()
		s.handleGetLogs(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("Search parameter filters results", func(t *testing.T) {
		logPath := writeTestLog(t, []string{"ERROR: fail", "INFO: ok", "ERROR: again"})

		s := &Server{
			config: config.Config{Log: config.LogConfig{Type: "file", FilePath: logPath}},
		}

		req := httptest.NewRequest("GET", "/api/logs?search=error", nil)
		rr := httptest.NewRecorder()
		s.handleGetLogs(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		var resp logResponse
		json.Unmarshal(rr.Body.Bytes(), &resp)
		assert.Len(t, resp.Lines, 2)
	})

	t.Run("Invalid log type returns 400", func(t *testing.T) {
		s := &Server{
			config: config.Config{Log: config.LogConfig{Type: "syslog"}},
		}

		req := httptest.NewRequest("GET", "/api/logs", nil)
		rr := httptest.NewRecorder()
		s.handleGetLogs(rr, req)

		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("File not found returns 500", func(t *testing.T) {
		s := &Server{
			config: config.Config{Log: config.LogConfig{Type: "file", FilePath: "/nonexistent/file.log"}},
		}

		req := httptest.NewRequest("GET", "/api/logs", nil)
		rr := httptest.NewRecorder()
		s.handleGetLogs(rr, req)

		assert.Equal(t, http.StatusInternalServerError, rr.Code)
	})
}

func TestReadJournalLogStub(t *testing.T) {
	// On non-Linux platforms, readJournalLog returns an error.
	// On Linux, this test still works but exercises the real implementation.
	lines, err := readJournalLog(context.Background(), "test-unit", 100, "")
	if err != nil {
		assert.Nil(t, lines)
		assert.Contains(t, err.Error(), "journal")
	}
	// If on Linux and journal is available, lines may be non-nil — that's OK.
}

func TestFileTooLargeError(t *testing.T) {
	err := &fileTooLargeError{size: 15 * 1024 * 1024}
	msg := err.Error()
	assert.Contains(t, msg, "15MB")
	assert.Contains(t, msg, "最大支持")
	assert.Contains(t, msg, "10MB")
}

func TestTruncateSearch(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		max     int
		want    string
	}{
		{"no truncation short", "hello", 10, "hello"},
		{"no truncation exact", "hello", 5, "hello"},
		{"empty input", "", 10, ""},
		{"ascii truncate", "abcdefgh", 5, "abcde"},
		{"utf8 boundary backup mid-rune", "你好世界", 5, "你"},
		{"utf8 boundary backup on boundary", "你好世界", 6, "你好"},
		{"utf8 backup lands on valid boundary", "你好世界", 7, "你好"},
		{"utf8 backup multiple bytes", "日本語テスト", 7, "日本"},
		{"all invalid bytes", string([]byte{0xff, 0xfe, 0xfd}), 2, ""},
		{"input contains valid U+FFFD", "ab\ufffdcd", 5, "ab\ufffd"},
		{"mixed ascii and multibyte", "abc你好", 4, "abc"},
		{"max zero", "abc", 0, ""},
		{"long ascii beyond max", strings.Repeat("x", 1000), maxSearchLength, strings.Repeat("x", maxSearchLength)},
		{"long multibyte beyond max", strings.Repeat("你", 500), maxSearchLength, strings.Repeat("你", maxSearchLength/3)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateSearch(tc.in, tc.max)
			assert.Equal(t, tc.want, got)
			assert.True(t, utf8.ValidString(got), "output must be valid UTF-8")
			assert.LessOrEqual(t, len(got), tc.max, "output byte length must not exceed max")
			// Must be a prefix of the original (in bytes).
			assert.True(t, strings.HasPrefix(tc.in, got), "output must be a byte prefix of input")
		})
	}
}
