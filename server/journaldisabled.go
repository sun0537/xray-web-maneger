//go:build linux && nojournal

package server

import (
	"context"
	"errors"
)

func readJournalLog(_ context.Context, _ string, _ int, _ string, _ int64) ([]string, error) {
	return nil, errors.New("当前构建已禁用 journal 日志读取功能（nojournal）")
}
