package server

import (
	"bufio"
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"xray-web-manager/middleware"
)

const (
	logDefaultLines   = 500
	logMaxLines       = 2000
	logMaxFileSize    = 10 * 1024 * 1024
	logTimeout        = 10 * time.Second
	maxSearchLength   = 256
)

// truncateSearch caps the byte length of a user-supplied search string and
// backs up past any incomplete trailing UTF-8 rune so the result is always
// valid UTF-8. Returns the (possibly shortened) string unchanged when it
// already fits within maxBytes.
func truncateSearch(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	s = s[:maxBytes]
	// If the cut happened at a rune boundary, we're already valid.
	if utf8.ValidString(s) {
		return s
	}
	// Otherwise back up past incomplete trailing rune(s). DecodeLastRuneInString
	// reports size>1 for the leading byte of an incomplete sequence, so we
	// drop the whole incomplete sequence in one step.
	for len(s) > 0 {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
		if utf8.ValidString(s) {
			break
		}
	}
	return s
}

type logResponse struct {
	Lines  []string `json:"lines"`
	Source string   `json:"source"`
	Error  string   `json:"error,omitempty"`
}

func (s *Server) handleGetLogs(w http.ResponseWriter, r *http.Request) {
	cfg := s.config.Log
	if cfg.Type == "none" || cfg.Type == "" {
		jsonResponse(w, logResponse{Lines: []string{}, Source: "none"}, http.StatusOK)
		return
	}

	linesParam := r.URL.Query().Get("lines")
	maxLines := logDefaultLines
	if linesParam != "" {
		if n, err := strconv.Atoi(linesParam); err == nil && n > 0 {
			maxLines = n
		}
	}
	if maxLines > logMaxLines {
		maxLines = logMaxLines
	}

	search := truncateSearch(strings.TrimSpace(r.URL.Query().Get("search")), maxSearchLength)

	ctx, cancel := context.WithTimeout(r.Context(), logTimeout)
	defer cancel()

	var lines []string
	var err error
	switch cfg.Type {
	case "file":
		lines, err = readLogFile(ctx, cfg.FilePath, maxLines, search)
	case "journal":
		lines, err = readJournalLog(ctx, cfg.Journal, maxLines, search)
	default:
		jsonError(w, "不支持的日志类型: "+cfg.Type, http.StatusBadRequest, "validation")
		return
	}

	if err != nil {
		log.Printf("读取日志失败 [来源: %s, 请求来源: %s]: %v", cfg.Type, middleware.GetClientIP(r), err)
		jsonError(w, "读取日志失败: "+err.Error(), http.StatusInternalServerError, "log_read")
		return
	}

	if lines == nil {
		lines = []string{}
	}
	jsonResponse(w, logResponse{Lines: lines, Source: cfg.Type}, http.StatusOK)
}

// readLogFile reads the last maxLines lines from a log file, optionally
// filtering by search substring. Uses a fixed-capacity ring buffer to
// avoid loading the entire file into memory.
//
// Ring buffer invariant:
//   - ring is always exactly maxLines in length once filled
//   - pos is the index where the NEXT line will be written (0..maxLines-1)
//   - count tracks total lines written (used to detect whether the buffer wrapped)
func readLogFile(ctx context.Context, filePath string, maxLines int, search string) ([]string, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, err
	}
	if info.Size() > logMaxFileSize {
		return nil, &fileTooLargeError{size: info.Size()}
	}

	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	ring := make([]string, maxLines)
	pos := 0
	count := 0

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)

	searchLower := strings.ToLower(search)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		line := scanner.Text()
		if search != "" && !strings.Contains(strings.ToLower(line), searchLower) {
			continue
		}
		ring[pos] = line
		pos = (pos + 1) % maxLines
		count++
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Fewer lines than capacity — return the filled portion in order.
	if count < maxLines {
		return ring[:count], nil
	}

	// Buffer wrapped — rotate so oldest line is first.
	result := make([]string, maxLines)
	n := copy(result, ring[pos:])
	copy(result[n:], ring[:pos])
	return result, nil
}

type fileTooLargeError struct {
	size int64
}

func (e *fileTooLargeError) Error() string {
	return "日志文件过大 (" + strconv.FormatInt(e.size/1024/1024, 10) + "MB), 最大支持 " + strconv.Itoa(logMaxFileSize/1024/1024) + "MB"
}
