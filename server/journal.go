//go:build linux && !nojournal

package server

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/coreos/go-systemd/v22/sdjournal"
)

func readJournalLog(ctx context.Context, unit string, maxLines int, search string, maxBytes int64) ([]string, error) {
	j, err := sdjournal.NewJournal()
	if err != nil {
		return nil, fmt.Errorf("打开 journal 失败: %w", err)
	}
	defer j.Close()

	matchField := "_SYSTEMD_UNIT=" + unit + ".service"
	if err := j.AddMatch(matchField); err != nil {
		return nil, fmt.Errorf("添加 journal 过滤条件失败: %w", err)
	}

	if err := j.SeekTail(); err != nil {
		return nil, fmt.Errorf("seek journal 尾部失败: %w", err)
	}

	searchLower := strings.ToLower(search)
	lines := make([]string, 0, maxLines)
	var totalBytes int64
	truncated := false

	for len(lines) < maxLines {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		n, err := j.Previous()
		if err != nil {
			return nil, fmt.Errorf("读取 journal 上一条记录失败: %w", err)
		}
		if n == 0 {
			break
		}

		msg, err := j.GetData("MESSAGE")
		if err != nil {
			log.Printf("跳过 journal 记录: %v", err)
			continue
		}
		msg = strings.TrimPrefix(msg, "MESSAGE=")

		if search != "" && !strings.Contains(strings.ToLower(msg), searchLower) {
			continue
		}

		usec, err := j.GetRealtimeUsec()
		var line string
		if err == nil {
			ts := time.Unix(int64(usec)/1e6, (int64(usec)%1e6)*1000).Format(time.RFC3339)
			line = ts + " " + msg
		} else {
			line = msg
		}

		lines = append(lines, line)

		// Enforce maxBytes when set (>0); keep the most-recent lines that
		// fit within the limit, matching readLogFile's behaviour.  When the
		// limit is exceeded, remove the oldest (lowest-index) lines until
		// the total fits.  At least the just-appended line is always kept;
		// a truncation marker is only prepended when older lines were
		// actually removed.
		if maxBytes > 0 {
			totalBytes += int64(len(line))
			origLen := len(lines)
			for totalBytes > maxBytes && len(lines) > 1 {
				totalBytes -= int64(len(lines[0]))
				// Clear the element reference before slicing so the old
				// string can be garbage-collected; lines[1:] alone only
				// moves the start pointer, keeping the backing array (and
				// all its earlier elements) alive.
				lines[0] = ""
				lines = lines[1:]
			}
			if len(lines) < origLen && totalBytes <= maxBytes {
				truncated = true
			}
			// Single line exceeds limit: kept as-is without marker,
			// matching readLogFile's single-line-exceeds-maxBytes path.
		}
	}

	for left, right := 0, len(lines)-1; left < right; left, right = left+1, right-1 {
		lines[left], lines[right] = lines[right], lines[left]
	}

	if truncated {
		lines = append([]string{"[日志输出过大，已截断]"}, lines...)
	}

	return lines, nil
}
