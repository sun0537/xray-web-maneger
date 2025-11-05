package sse

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestManager_AddRemoveCount (基础功能测试)
func TestManager_AddRemoveCount(t *testing.T) {
	mgr := NewManager()
	assert.Equal(t, 0, mgr.Count(), "初始 Count 应为 0")

	// 添加连接
	conn1, ctx1 := mgr.Add(context.Background())
	assert.NotNil(t, conn1)
	assert.NotNil(t, ctx1)
	assert.Equal(t, 1, mgr.Count())

	conn2, _ := mgr.Add(context.Background())
	assert.Equal(t, 2, mgr.Count())

	// 移除连接
	mgr.Remove(conn1)
	assert.Equal(t, 1, mgr.Count(), "移除 conn1 后 Count 应为 1")

	mgr.Remove(conn2)
	assert.Equal(t, 0, mgr.Count(), "移除 conn2 后 Count 应为 0")
}

// TestManager_CloseAll (优雅关闭测试)
func TestManager_CloseAll(t *testing.T) {
	mgr := NewManager()

	// 添加一个连接并监听它的 Context
	_, ctx1 := mgr.Add(context.Background())
	conn1Closed := make(chan bool)
	go func() {
		<-ctx1.Done() // 等待上下文被取消
		conn1Closed <- true
	}()

	// 添加第二个连接
	mgr.Add(context.Background())
	assert.Equal(t, 2, mgr.Count())

	// 执行关闭
	mgr.CloseAll()

	// 1. 验证 Count 是否立即归零
	assert.Equal(t, 0, mgr.Count())

	// 2. 验证新连接是否被拒绝
	conn3, _ := mgr.Add(context.Background())
	assert.Nil(t, conn3, "关闭后 Add 应返回 nil")
	assert.Equal(t, 0, mgr.Count())

	// 3. 验证已有的连接是否收到了关闭信号 (ctx.Done())
	select {
	case <-conn1Closed:
		// 测试通过
	case <-time.After(1 * time.Second):
		t.Fatal("超时：conn1 的 Context 在 CloseAll 后未被取消")
	}
}

// TestManager_Race (并发安全测试)
func TestManager_Race(t *testing.T) {
	mgr := NewManager()
	var wg sync.WaitGroup

	// 设置并发的 Goroutine 数量
	numGoroutines := 100
	wg.Add(numGoroutines)

	// 同时运行 100 个 goroutine
	for i := 0; i < numGoroutines; i++ {
		go func(i int) {
			defer wg.Done()

			// 模拟并发添加
			conn, _ := mgr.Add(context.Background())
			if conn == nil { // 可能在 CloseAll 之后
				return
			}

			// 随机做一些操作 (读取)
			mgr.Count()

			// 随机移除
			if i%3 == 0 {
				mgr.Remove(conn)
			}

			// Goroutine 完成了添加/读取/移除操作后就应该立即退出。

		}(i)
	}

	// 在并发操作期间，随机触发 CloseAll
	time.Sleep(20 * time.Millisecond)
	mgr.CloseAll()

	// 等待所有 goroutine 退出
	wg.Wait()

	assert.Equal(t, 0, mgr.Count(), "在 CloseAll 和并发操作后，Count 最终应为 0")
}
