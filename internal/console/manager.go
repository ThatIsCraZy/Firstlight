package console

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

type managedSession struct {
	session  *Session
	lastUsed time.Time
}

type openOperation struct {
	key        string
	done       chan struct{}
	handle     string
	state      State
	err        error
	finishedAt time.Time
}

type Manager struct {
	ctx            context.Context
	cancel         context.CancelFunc
	sessionTTL     time.Duration
	connectTimeout time.Duration
	isoRoot        *ISORoot
	logf           func(format string, args ...any)

	mu        sync.Mutex
	sessions  map[string]*managedSession
	openOps   map[string]*openOperation
	closed    bool
	closeOnce sync.Once
	done      chan struct{}
}

func NewManager(parent context.Context, opts ManagerOptions) *Manager {
	if parent == nil {
		parent = context.Background()
	}
	if opts.SessionTTL <= 0 {
		opts.SessionTTL = DefaultSessionTTL
	}
	if opts.ConnectTimeout <= 0 {
		opts.ConnectTimeout = DefaultConnectTimeout
	}
	ctx, cancel := context.WithCancel(parent)
	m := &Manager{
		ctx:            ctx,
		cancel:         cancel,
		sessionTTL:     opts.SessionTTL,
		connectTimeout: opts.ConnectTimeout,
		isoRoot:        opts.ISORoot,
		logf:           opts.Logger,
		sessions:       make(map[string]*managedSession),
		openOps:        make(map[string]*openOperation),
		done:           make(chan struct{}),
	}
	go m.sweep()
	return m
}

func (m *Manager) OpenOnce(ctx context.Context, operationID, key string, opts OpenOptions) (string, State, error) {
	if operationID == "" {
		return "", State{}, errors.New("operation_id is required")
	}
	if len(operationID) > 128 {
		return "", State{}, errors.New("operation_id must not exceed 128 characters")
	}
	m.mu.Lock()
	if existing := m.openOps[operationID]; existing != nil {
		if existing.key != key {
			m.mu.Unlock()
			return "", State{}, errors.New("operation_id was already used with different arguments")
		}
		done := existing.done
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return "", State{}, ctx.Err()
		case <-done:
			return existing.handle, existing.state, existing.err
		}
	}
	record := &openOperation{key: key, done: make(chan struct{})}
	m.openOps[operationID] = record
	m.mu.Unlock()

	handle, state, err := m.Open(ctx, opts)
	m.mu.Lock()
	record.handle, record.state, record.err = handle, state, err
	record.finishedAt = time.Now()
	close(record.done)
	m.mu.Unlock()
	return handle, state, err
}

func (m *Manager) Open(ctx context.Context, opts OpenOptions) (string, State, error) {
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return "", State{}, errors.New("console manager is closed")
	}
	connectCtx, cancel := context.WithTimeout(ctx, m.connectTimeout)
	stop := context.AfterFunc(m.ctx, cancel)
	session, err := openSession(m.ctx, connectCtx, opts, m.isoRoot, m.logf)
	stop()
	cancel()
	if err != nil {
		return "", State{}, err
	}
	handle, err := newHandle()
	if err != nil {
		_ = session.Close()
		return "", State{}, err
	}
	session.setHandle(handle)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		_ = session.Close()
		return "", State{}, errors.New("console manager is closed")
	}
	m.sessions[handle] = &managedSession{session: session, lastUsed: time.Now()}
	m.mu.Unlock()
	if m.logf != nil {
		m.logf("console opened address=%q insecure_tls=%v", opts.Address, opts.InsecureSkipVerify)
	}
	return handle, session.State(), nil
}

func (m *Manager) Get(handle string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.sessions[handle]
	if entry == nil {
		return nil, ErrUnknownHandle
	}
	entry.lastUsed = time.Now()
	return entry.session, nil
}

func (m *Manager) CloseSession(handle string) (bool, error) {
	m.mu.Lock()
	entry := m.sessions[handle]
	delete(m.sessions, handle)
	m.mu.Unlock()
	if entry == nil {
		return false, nil
	}
	if m.logf != nil {
		m.logf("console closed")
	}
	return true, entry.session.Close()
}

func (m *Manager) Close() error {
	var closeErr error
	m.closeOnce.Do(func() {
		m.cancel()
		m.mu.Lock()
		m.closed = true
		entries := make([]*managedSession, 0, len(m.sessions))
		for _, entry := range m.sessions {
			entries = append(entries, entry)
		}
		m.sessions = make(map[string]*managedSession)
		m.openOps = make(map[string]*openOperation)
		m.mu.Unlock()
		for _, entry := range entries {
			closeErr = errors.Join(closeErr, entry.session.Close())
		}
		<-m.done
	})
	return closeErr
}

func (m *Manager) sweep() {
	defer close(m.done)
	interval := m.sessionTTL / 2
	if interval > time.Minute {
		interval = time.Minute
	}
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case now := <-ticker.C:
			var expired []struct {
				handle string
				entry  *managedSession
			}
			m.mu.Lock()
			for handle, entry := range m.sessions {
				if now.Sub(entry.lastUsed) >= m.sessionTTL {
					expired = append(expired, struct {
						handle string
						entry  *managedSession
					}{handle: handle, entry: entry})
					delete(m.sessions, handle)
				}
			}
			for operationID, record := range m.openOps {
				if !record.finishedAt.IsZero() && now.Sub(record.finishedAt) >= m.sessionTTL {
					delete(m.openOps, operationID)
				}
			}
			m.mu.Unlock()
			for _, item := range expired {
				if m.logf != nil {
					m.logf("console expired")
				}
				_ = item.entry.session.Close()
			}
		}
	}
}

func newHandle() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}
