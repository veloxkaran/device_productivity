package cloud

import (
	"encoding/json"
	"my-monitor/capture"
	"my-monitor/storage"
	"os"
	"strings"
	"sync"
	"time"
)

const ConfigPath = "data/cloud.json"

type Manager struct {
	mu     sync.Mutex
	db     *storage.DB
	cfg    Config
	syncer *Syncer
	stop   chan struct{}
	kick   chan struct{}
}

func NewManager(db *storage.DB) *Manager {
	m := &Manager{db: db}
	if b, err := os.ReadFile(ConfigPath); err == nil {
		var cfg Config
		if json.Unmarshal(b, &cfg) == nil {
			m.Apply(cfg, false)
		}
	}
	return m
}

func (m *Manager) Config() Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg
}

func (m *Manager) Apply(cfg Config, persist bool) error {
	cfg.URL = strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
	cfg.SyncToken = strings.TrimSpace(cfg.SyncToken)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stop != nil {
		close(m.stop)
		m.stop = nil
	}
	m.cfg = cfg
	m.syncer = nil
	if persist {
		b, _ := json.MarshalIndent(cfg, "", "  ")
		if err := os.WriteFile(ConfigPath, b, 0600); err != nil {
			return err
		}
	}
	if cfg.URL == "" || cfg.SyncToken == "" {
		return nil
	}
	m.syncer = NewSyncer(cfg, m.db)
	capture.OnCaptured = m.SyncNow
	m.stop = make(chan struct{})
	m.kick = make(chan struct{}, 1)
	go m.syncer.loop(time.Minute, m.stop, m.kick)
	go m.syncer.heartbeatLoop(20*time.Second, m.stop)
	return nil
}

func (m *Manager) Disconnect() error {
	m.mu.Lock()
	s := m.syncer
	m.mu.Unlock()
	if s != nil {
		s.sendHeartbeatNow()
		s.SendOffline()
	}
	return m.Apply(Config{}, true)
}

func (m *Manager) SyncNow() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.syncer == nil {
		return
	}
	go m.syncer.sendHeartbeat()
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

func (s *Syncer) sendHeartbeatNow() { s.run() }

func (m *Manager) Flush() {
	m.mu.Lock()
	s := m.syncer
	m.mu.Unlock()
	if s != nil {
		s.run()
		s.SendOffline()
	}
}
