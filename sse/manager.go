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

const defaultMaxConns = 256

type Manager struct {
	mu          sync.RWMutex
	connections map[Connection]struct{}
	shutdown    bool
	maxConns    int
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
		connections: make(map[Connection]struct{}),
		maxConns:    defaultMaxConns,
	}
}

func (m *Manager) Add(ctx context.Context) Connection {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.shutdown {
		return nil
	}

	if len(m.connections) >= m.maxConns {
		log.Printf("SSE 连接数已达上限 (%d)，拒绝新连接", m.maxConns)
		return nil
	}

	newCtx, cancel := context.WithCancel(ctx)
	localConn := &sseConnection{
		ctx:    newCtx,
		cancel: cancel,
	}
	m.connections[localConn] = struct{}{}

	return localConn
}

func (m *Manager) Remove(conn Connection) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if conn != nil {
		delete(m.connections, conn)
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

	m.connections = make(map[Connection]struct{})
	log.Println("所有 SSE 连接已关闭")
}

func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.connections)
}
