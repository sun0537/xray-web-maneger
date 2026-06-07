//go:build !linux

package server

import (
	"context"
	"errors"
)

func readJournalLog(_ context.Context, _ string, _ int, _ string, _ int64) ([]string, error) {
	return nil, errors.New("journal 日志源仅支持 Linux 系统")
}
