package sse

import (
	"context"
	"log"
	"sync"
)

type Connection interface {
	Context() context.Context
	Close()
}

type Manager struct {
	mu          sync.RWMutex
	connections map[Connection]bool
	shutdown    bool
}

type sseConnection struct {
	ctx    context.Context
	cancel context.CancelFunc
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
		connections: make(map[Connection]bool),
	}
}

func (m *Manager) Add(ctx context.Context) (Connection, context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.shutdown {
		// 如果已经在关闭，不接受新连接
		return nil, ctx
	}

	newCtx, cancel := context.WithCancel(ctx)
	conn := &sseConnection{
		ctx:    newCtx,
		cancel: cancel,
	}
	m.connections[conn] = true

	log.Printf("SSE 连接已添加，当前连接数: %d", len(m.connections))
	return conn, newCtx
}

func (m *Manager) Remove(conn Connection) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if conn != nil {
		delete(m.connections, conn)
		log.Printf("SSE 连接已移除，当前连接数: %d", len(m.connections))
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
