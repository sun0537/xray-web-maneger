package sse

import (
	"context"
	"log"
	"sync"
	"time"
)

type Connection interface {
	Context() context.Context
	Close()
}

type Manager struct {
	mu              sync.RWMutex
	connections     map[Connection]bool
	shutdown        bool
	cleanupInterval time.Duration
	cleanupTicker   *time.Ticker
}

type sseConnection struct {
	ctx          context.Context
	cancel       context.CancelFunc
	lastActivity time.Time
}

func (c *sseConnection) Context() context.Context {
	return c.ctx
}

func (c *sseConnection) Close() {
	if c.cancel != nil {
		c.cancel()
	}
}

func NewManager() *Manager {
	return &Manager{
		connections:     make(map[Connection]bool),
		cleanupInterval: 10 * time.Second,
	}
}

func (m *Manager) Add(ctx context.Context) (Connection, context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.shutdown {
		return nil, ctx
	}

	newCtx, cancel := context.WithCancel(ctx)
	localConn := &sseConnection{
		ctx:          newCtx,
		cancel:       cancel,
		lastActivity: time.Now(),
	}
	m.connections[localConn] = true

	log.Printf("SSE 连接已添加，当前连接数: %d", len(m.connections))
	return localConn, newCtx
}

func (m *Manager) Remove(conn Connection) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if conn != nil {
		delete(m.connections, conn)
		log.Printf("SSE 连接已移除，当前连接数: %d", len(m.connections))
	}
}

func (m *Manager) StartCleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cleanupTicker != nil {
		return
	}

	m.cleanupTicker = time.NewTicker(m.cleanupInterval)
	go func() {
		for range m.cleanupTicker.C {
			m.cleanupExpiredConnections()
		}
	}()
	log.Println("SSE 清理机制已启动")
}

func (m *Manager) StopCleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cleanupTicker != nil {
		m.cleanupTicker.Stop()
		m.cleanupTicker = nil
	}
	log.Println("SSE 清理机制已停止")
}

func (m *Manager) cleanupExpiredConnections() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	removedCount := 0

	for conn := range m.connections {
		sseConn, ok := conn.(*sseConnection)
		if !ok {
			continue
		}

		if now.Sub(sseConn.lastActivity) > 30*time.Second {
			conn.Close()
			delete(m.connections, conn)
			removedCount++
		}
	}

	if removedCount > 0 {
		log.Printf("清理了 %d 个超时 SSE 连接，当前连接数: %d", removedCount, len(m.connections))
	}
}

func (m *Manager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.shutdown = true
	log.Printf("正在关闭 %d 个 SSE 连接...", len(m.connections))

	for conn := range m.connections {
		conn.Close()
	}

	// 清空连接列表
	m.connections = make(map[Connection]bool)
	log.Println("所有 SSE 连接已关闭")
}

func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.connections)
}
