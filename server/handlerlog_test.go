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

		lines, err := readLogFile(context.Background(), path, 100, "", 0)
		assert.NoError(t, err)
		assert.Equal(t, []string{"line1", "line2", "line3"}, lines)
	})

	t.Run("Respects maxLines ring buffer", func(t *testing.T) {
		path := writeTestLog(t, []string{"a", "b", "c", "d", "e"})

		lines, err := readLogFile(context.Background(), path, 3, "", 0)
		assert.NoError(t, err)
		assert.Equal(t, []string{"c", "d", "e"}, lines)
	})

	t.Run("Ring buffer exact capacity", func(t *testing.T) {
		path := writeTestLog(t, []string{"1", "2", "3"})

		lines, err := readLogFile(context.Background(), path, 3, "", 0)
		assert.NoError(t, err)
		assert.Equal(t, []string{"1", "2", "3"}, lines)
	})

	t.Run("Ring buffer with more than double capacity", func(t *testing.T) {
		all := make([]string, 20)
		for i := range all {
			all[i] = "line" + string(rune('a'+i))
		}
		path := writeTestLog(t, all)

		lines, err := readLogFile(context.Background(), path, 5, "", 0)
		assert.NoError(t, err)
		assert.Len(t, lines, 5)
		assert.Equal(t, "linep", lines[0])
		assert.Equal(t, "linet", lines[4])
	})

	t.Run("Search filter", func(t *testing.T) {
		path := writeTestLog(t, []string{"hello world", "foo bar", "hello again"})

		lines, err := readLogFile(context.Background(), path, 100, "hello", 0)
		assert.NoError(t, err)
		assert.Equal(t, []string{"hello world", "hello again"}, lines)
	})

	t.Run("Search is case-insensitive", func(t *testing.T) {
		path := writeTestLog(t, []string{"ERROR: something", "info: ok", "error: another"})

		lines, err := readLogFile(context.Background(), path, 100, "error", 0)
		assert.NoError(t, err)
		assert.Equal(t, []string{"ERROR: something", "error: another"}, lines)
	})

	t.Run("Search with maxLines ring buffer", func(t *testing.T) {
		path := writeTestLog(t, []string{"match1", "skip", "match2", "skip", "match3"})

		lines, err := readLogFile(context.Background(), path, 2, "match", 0)
		assert.NoError(t, err)
		assert.Equal(t, []string{"match2", "match3"}, lines)
	})

	t.Run("Empty file", func(t *testing.T) {
		path := writeTestLog(t, []string{""})

		lines, err := readLogFile(context.Background(), path, 100, "", 0)
		assert.NoError(t, err)
		assert.Equal(t, []string{""}, lines)
	})

	t.Run("File not found", func(t *testing.T) {
		_, err := readLogFile(context.Background(), "/nonexistent/path.log", 100, "", 0)
		assert.Error(t, err)
	})

	t.Run("Cancelled context", func(t *testing.T) {
		path := writeTestLog(t, []string{"line1", "line2", "line3"})

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := readLogFile(ctx, path, 100, "", 0)
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

	_, err := readLogFile(context.Background(), path, 100, "", 0)
	assert.Error(t, err)

	var ftl *fileTooLargeError
	if assert.ErrorAs(t, err, &ftl) {
		msg := ftl.Error()
		assert.Contains(t, msg, "日志文件过大")
		assert.Contains(t, msg, "最大支持")
	}
}

func TestReadLogFileMaxBytesTruncation(t *testing.T) {
	// maxBytes=0 disables truncation (existing tests already cover this).
	t.Run("maxBytes larger than total — no truncation", func(t *testing.T) {
		path := writeTestLog(t, []string{"a short line", "another short line"})
		lines, err := readLogFile(context.Background(), path, 100, "", 1<<20)
		assert.NoError(t, err)
		assert.Equal(t, []string{"a short line", "another short line"}, lines)
	})

	t.Run("maxBytes truncates — marker prepended, most-recent lines kept", func(t *testing.T) {
		// Each line ~12 bytes, maxBytes=20 should keep last 1-2 lines.
		path := writeTestLog(t, []string{"line 0", "line 1", "line 2", "line 3", "line 4"})
		lines, err := readLogFile(context.Background(), path, 100, "", 20)
		assert.NoError(t, err)
		assert.Greater(t, len(lines), 1, "should have marker + at least one line")
		assert.Equal(t, "[日志输出过大，已截断]", lines[0], "marker must be first")
		// The last line ("line 4") should always be present.
		assert.Equal(t, "line 4", lines[len(lines)-1])
	})

	t.Run("maxBytes so small only last line fits (multi-line result)", func(t *testing.T) {
		path := writeTestLog(t, []string{"x", "y", "z"})
		// maxBytes=3: "z" (1 byte) fits but "y" (1) + "z" (1) = 2 also fits.
		// maxBytes=1: only "z" fits, "y"+"z"=2 > 1
		lines, err := readLogFile(context.Background(), path, 100, "", 1)
		assert.NoError(t, err)
		assert.Len(t, lines, 2, "marker + last line")
		assert.Equal(t, "[日志输出过大，已截断]", lines[0])
		assert.Equal(t, "z", lines[1])
	})

	t.Run("single line fits within maxBytes", func(t *testing.T) {
		path := writeTestLog(t, []string{"hello"})
		lines, err := readLogFile(context.Background(), path, 100, "", 100)
		assert.NoError(t, err)
		assert.Equal(t, []string{"hello"}, lines)
	})

	t.Run("single line exceeds maxBytes — still kept, no marker", func(t *testing.T) {
		path := writeTestLog(t, []string{"a very long single line that exceeds the limit"})
		lines, err := readLogFile(context.Background(), path, 100, "", 2)
		assert.NoError(t, err)
		// Only one line in result, and it alone exceeds maxBytes → clamp
		// sets kept=0, and kept>0 is false, so line is returned as-is.
		assert.Equal(t, []string{"a very long single line that exceeds the limit"}, lines)
	})

	t.Run("maxBytes truncation with ring buffer wrapping", func(t *testing.T) {
		// 6 lines, maxLines=3 → ring buffer wraps → result is last 3 lines
		all := make([]string, 6)
		for i := range all {
			all[i] = strings.Repeat("x", 20) // 20 bytes each
		}
		path := writeTestLog(t, all)

		// maxBytes=25: only the last line (20 bytes) fits. Line-1 (40) > 25.
		lines, err := readLogFile(context.Background(), path, 3, "", 25)
		assert.NoError(t, err)
		assert.Len(t, lines, 2, "marker + one line")
		assert.Equal(t, "[日志输出过大，已截断]", lines[0])
	})
}

func TestTrimLinesToBytes(t *testing.T) {
	t.Run("maxBytes zero returns unchanged", func(t *testing.T) {
		lines := []string{"a", "b"}
		got := trimLinesToBytes(lines, 0)
		assert.Equal(t, lines, got)
	})

	t.Run("maxBytes negative returns unchanged", func(t *testing.T) {
		lines := []string{"a", "b"}
		got := trimLinesToBytes(lines, -1)
		assert.Equal(t, lines, got)
	})

	t.Run("empty slice returns unchanged", func(t *testing.T) {
		got := trimLinesToBytes(nil, 100)
		assert.Nil(t, got)
	})

	t.Run("all lines fit — no marker", func(t *testing.T) {
		lines := []string{"hello", "world"} // 5+5=10 bytes
		got := trimLinesToBytes(lines, 10)
		assert.Equal(t, lines, got)
	})

	t.Run("all lines fit with room to spare", func(t *testing.T) {
		lines := []string{"a", "b"} // 1+1=2 bytes
		got := trimLinesToBytes(lines, 100)
		assert.Equal(t, lines, got)
	})

	t.Run("keeps most-recent lines that fit", func(t *testing.T) {
		// "aa"(2) + "bb"(2) + "cc"(2) = 6 bytes total.
		// maxBytes=5: "bb"+"cc"=4 fits, "aa"+"bb"+"cc"=6 doesn't.
		lines := []string{"aa", "bb", "cc"}
		got := trimLinesToBytes(lines, 5)
		assert.Equal(t, []string{truncationMarker, "bb", "cc"}, got)
	})

	t.Run("only last line fits", func(t *testing.T) {
		lines := []string{"aaa", "bbb", "c"} // 3+3+1=7
		// maxBytes=1: only "c"(1) fits.
		got := trimLinesToBytes(lines, 1)
		assert.Equal(t, []string{truncationMarker, "c"}, got)
	})

	t.Run("single line fits — no marker", func(t *testing.T) {
		got := trimLinesToBytes([]string{"hello"}, 100)
		assert.Equal(t, []string{"hello"}, got)
	})

	t.Run("single line exactly at limit — no marker", func(t *testing.T) {
		got := trimLinesToBytes([]string{"hello"}, 5) // 5 bytes
		assert.Equal(t, []string{"hello"}, got)
	})

	t.Run("single line exceeds maxBytes — kept as-is, no marker", func(t *testing.T) {
		lines := []string{"a very long single line"}
		got := trimLinesToBytes(lines, 2)
		// Only one line; start would stay at len(lines), so condition fails.
		assert.Equal(t, lines, got)
	})

	t.Run("exact boundary — all lines just fit", func(t *testing.T) {
		lines := []string{"ab", "cd", "ef"} // 2+2+2=6
		got := trimLinesToBytes(lines, 6)
		assert.Equal(t, lines, got)
	})

	t.Run("off-by-one: one byte over forces drop of oldest", func(t *testing.T) {
		lines := []string{"ab", "cd", "ef"} // 2+2+2=6
		got := trimLinesToBytes(lines, 5)
		assert.Equal(t, []string{truncationMarker, "cd", "ef"}, got)
	})

	t.Run("mixed-length lines", func(t *testing.T) {
		lines := []string{"x", "yyyy", "zz"} // 1+4+2=7
		// maxBytes=6: "yyyy"+"zz"=6 fits, all 7 doesn't.
		got := trimLinesToBytes(lines, 6)
		assert.Equal(t, []string{truncationMarker, "yyyy", "zz"}, got)
	})

	t.Run("multibyte UTF-8 lines", func(t *testing.T) {
		// "你好" = 6 bytes, "世界" = 6 bytes
		lines := []string{"你好", "世界"}
		got := trimLinesToBytes(lines, 6)
		assert.Equal(t, []string{truncationMarker, "世界"}, got)
	})

	t.Run("multibyte UTF-8 all fit", func(t *testing.T) {
		lines := []string{"你好", "世界"}
		got := trimLinesToBytes(lines, 12)
		assert.Equal(t, lines, got)
	})

	t.Run("many lines — drops correct number of oldest", func(t *testing.T) {
		// 5 lines of 10 bytes each = 50 total.
		lines := []string{
			"aaaaaaaaaa", // 10
			"bbbbbbbbbb", // 10
			"cccccccccc", // 10
			"dddddddddd", // 10
			"eeeeeeeeee", // 10
		}
		// maxBytes=30: last 3 lines (30 bytes) fit.
		got := trimLinesToBytes(lines, 30)
		assert.Equal(t, []string{truncationMarker, "cccccccccc", "dddddddddd", "eeeeeeeeee"}, got)
	})

	t.Run("does not mutate input slice", func(t *testing.T) {
		lines := []string{"aa", "bb", "cc"}
		_ = trimLinesToBytes(lines, 3)
		assert.Equal(t, []string{"aa", "bb", "cc"}, lines, "original must be unchanged")
	})
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
	lines, err := readJournalLog(context.Background(), "test-unit", 100, "", 1024)
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
		name string
		in   string
		max  int
		want string
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
