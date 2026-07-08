package server

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
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
	truncationMarker  = "[日志输出过大，已截断]"
)

// trimLinesToBytes keeps the most-recent lines that fit within maxBytes,
// prepending a truncation marker when older lines were dropped. A single
// line that exceeds maxBytes is returned as-is to avoid corrupting its
// content with a partial truncation.
func trimLinesToBytes(lines []string, maxBytes int64) []string {
	if maxBytes <= 0 || len(lines) == 0 {
		return lines
	}
	start := len(lines)
	var used int64
	for i := len(lines) - 1; i >= 0; i-- {
		used += int64(len(lines[i]))
		if used > maxBytes {
			break
		}
		start = i
	}
	if start > 0 && start < len(lines) {
		return append([]string{truncationMarker}, lines[start:]...)
	}
	return lines
}

// ringPool reuses []string slices for the log ring buffer to reduce GC pressure.
var ringPool = sync.Pool{
	New: func() any {
		s := make([]string, 0, logMaxLines)
		return &s
	},
}

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
		lines, err = readLogFile(ctx, cfg.FilePath, maxLines, search, s.config.Log.MaxBytes)
	case "journal":
		lines, err = readJournalLog(ctx, cfg.Journal, maxLines, search, s.config.Log.MaxBytes)
	default:
		log.Printf("不支持的日志类型: %s [请求来源: %s]", cfg.Type, middleware.ClientIPFromContext(r))
		jsonError(w, "不支持的日志类型", http.StatusBadRequest, "validation")
		return
	}

	if err != nil {
		log.Printf("读取日志失败 [来源: %s, 请求来源: %s]: %v", cfg.Type, middleware.ClientIPFromContext(r), err)
		jsonError(w, "读取日志失败", http.StatusInternalServerError, "log_read")
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
func readLogFile(ctx context.Context, filePath string, maxLines int, search string, maxBytes int64) ([]string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > logMaxFileSize {
		return nil, &fileTooLargeError{size: info.Size()}
	}

	// Reuse ring buffer from pool to reduce GC pressure.
	ringPtr := ringPool.Get().(*[]string)
	fromPool := cap(*ringPtr) >= maxLines
	var ring []string
	if fromPool {
		ring = (*ringPtr)[:maxLines]
	} else {
		ring = make([]string, maxLines)
	}
	defer func() {
		if fromPool {
			// Reset length before returning to pool. Element references are
			// harmless: the result slice is now always an independent copy,
			// and the pool is bounded anyway.
			*ringPtr = ring[:0]
			ringPool.Put(ringPtr)
		}
	}()
	pos := 0
	count := 0

	scanner := bufio.NewScanner(f)
	// Use a smaller initial buffer to reduce allocation for small files.
	scanner.Buffer(make([]byte, 0, 4*1024), 256*1024)

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
		return nil, fmt.Errorf("扫描日志文件 %s 失败: %w", filePath, err)
	}

	// Fewer lines than capacity — copy to a fresh slice so the result does
	// not share the pool's backing array. Without the copy, a concurrent
	// goroutine that obtains the same pool entry could overwrite elements
	// while the caller is still reading result (e.g. JSON serialization).
	var result []string
	if count < maxLines {
		result = make([]string, count)
		copy(result, ring[:count])
	} else {
		// Buffer wrapped — rotate so oldest line is first.
		result = make([]string, maxLines)
		n := copy(result, ring[pos:])
		copy(result[n:], ring[:pos])
	}

	if maxBytes > 0 {
		result = trimLinesToBytes(result, maxBytes)
	}
	return result, nil
}

type fileTooLargeError struct {
	size int64
}

func (e *fileTooLargeError) Error() string {
	return "日志文件过大 (" + strconv.FormatInt(e.size/1024/1024, 10) + "MB), 最大支持 " + strconv.Itoa(logMaxFileSize/1024/1024) + "MB"
}
