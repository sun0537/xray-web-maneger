//go:build linux && !nojournal

package server

import (
	"context"
	"fmt"
	"log"
	"slices"
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
	}

	slices.Reverse(lines)

	if maxBytes > 0 {
		lines = trimLinesToBytes(lines, maxBytes)
	}

	return lines, nil
}
