package session

import (
	"context"
	"errors"
	"sync"

	"github.com/GrainedLotus515/gobard/internal/playback"
)

var ErrGuildLimit = errors.New("guild session limit reached")
var ErrExists = errors.New("guild session already exists")

type Defaults struct {
	Volume     int
	Ducking    bool
	DuckTarget int
}
type managedSession struct {
	session  *Session
	removing bool
}

// Manager owns bounded guild admission and shutdown. Registry locks only cover
// map/state updates; construction, operations, cancellation and joins happen
// outside them. Closing a guild retains its admission slot until it is joined.
type Manager struct {
	ctx                      context.Context
	cancel                   context.CancelFunc
	engine                   *playback.Engine
	defaults                 Defaults
	maxGuilds, queueCapacity int
	mu                       sync.Mutex
	entries                  map[string]*managedSession
	stopping                 bool
	work                     sync.WaitGroup
	closeOnce                sync.Once
}

func NewManager(parent context.Context, engine *playback.Engine, maxGuilds, queueCapacity int, defaults Defaults) (*Manager, error) {
	if parent == nil || engine == nil || maxGuilds < 1 || maxGuilds > 1024 || queueCapacity < 1 || queueCapacity > MaxQueue || defaults.Volume < 0 || defaults.Volume > 100 || defaults.DuckTarget < 0 || defaults.DuckTarget > 100 {
		return nil, errors.New("invalid session manager configuration")
	}
	// #nosec G118 -- Manager.Close cancels and joins every admitted session.
	ctx, cancel := context.WithCancel(parent)
	return &Manager{ctx: ctx, cancel: cancel, engine: engine, defaults: defaults, maxGuilds: maxGuilds, queueCapacity: queueCapacity, entries: make(map[string]*managedSession)}, nil
}

// Create admits a new guild. A rejected output remains caller-owned. Concurrent
// duplicate creation returns ErrExists; callers use Get after initial creation.
// Outputs always remain adapter-owned; close the manager before closing them.
func (m *Manager) Create(guild string, output playback.Output) (*Session, error) {
	if guild == "" || len(guild) > 64 || output == nil {
		return nil, errors.New("guild and output required")
	}
	m.mu.Lock()
	if m.stopping || m.ctx.Err() != nil {
		m.mu.Unlock()
		return nil, ErrClosed
	}
	if _, ok := m.entries[guild]; ok {
		m.mu.Unlock()
		return nil, ErrExists
	}
	if len(m.entries) >= m.maxGuilds {
		m.mu.Unlock()
		return nil, ErrGuildLimit
	}
	e := &managedSession{}
	m.entries[guild] = e
	m.work.Add(1)
	m.mu.Unlock()
	defer m.work.Done()
	s, err := New(m.ctx, guild, m.engine, output, m.queueCapacity)
	if err == nil {
		err = m.configure(s)
	}
	m.mu.Lock()
	if m.stopping || m.ctx.Err() != nil {
		err = ErrClosed
	}
	if err != nil {
		m.mu.Unlock()
		if s != nil {
			s.Close()
		}
		m.mu.Lock()
		delete(m.entries, guild)
		m.mu.Unlock()
		return nil, err
	}
	e.session = s
	m.mu.Unlock()
	return s, nil
}

func (m *Manager) configure(s *Session) error {
	for _, op := range []Operation{{Action: Volume, Position: m.defaults.Volume}, {Action: DuckTarget, Position: m.defaults.DuckTarget}, {Action: Ducking, Enabled: m.defaults.Ducking}} {
		if _, err := s.Apply(m.ctx, op); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) Get(guild string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[guild]
	if !ok || e.removing || e.session == nil || m.stopping || m.ctx.Err() != nil {
		return nil, false
	}
	return e.session, true
}

func (m *Manager) Remove(guild string) error {
	m.mu.Lock()
	if m.stopping {
		m.mu.Unlock()
		return ErrClosed
	}
	e, ok := m.entries[guild]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	if e.removing || e.session == nil {
		m.mu.Unlock()
		return ErrBusy
	}
	e.removing = true
	m.work.Add(1)
	m.mu.Unlock()
	defer m.work.Done()
	e.session.Close()
	m.mu.Lock()
	delete(m.entries, guild)
	m.mu.Unlock()
	return nil
}

func (m *Manager) Close() {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.stopping = true
		m.mu.Unlock()
		m.cancel()
		m.work.Wait()
		// No creators/removers can mutate entries after the barrier.
		for _, e := range m.entries {
			if e.session != nil {
				e.session.Close()
			}
		}
		m.mu.Lock()
		clear(m.entries)
		m.mu.Unlock()
	})
}
