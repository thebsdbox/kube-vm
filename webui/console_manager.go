package webui

import (
	"errors"
	"net"
	"sync"
)

type consoleSession struct {
	uid        string
	conn       net.Conn
	mu         sync.Mutex
	buf        []byte
	baseOffset int64
	closed     bool
}

func (s *consoleSession) append(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.buf = append(s.buf, data...)
	if len(s.buf) > 1<<20 {
		trim := len(s.buf) - (1 << 20)
		s.buf = append([]byte(nil), s.buf[trim:]...)
		s.baseOffset += int64(trim)
	}
}

func (s *consoleSession) poll(offset int64) ([]byte, bool, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if offset < s.baseOffset {
		offset = s.baseOffset
	}
	start := int(offset - s.baseOffset)
	if start < 0 {
		start = 0
	}
	if start > len(s.buf) {
		start = len(s.buf)
	}
	out := make([]byte, len(s.buf)-start)
	copy(out, s.buf[start:])
	return out, s.closed, s.baseOffset + int64(len(s.buf))
}

func (s *consoleSession) write(p []byte) error {
	s.mu.Lock()
	closed := s.closed
	conn := s.conn
	s.mu.Unlock()
	if closed || conn == nil {
		return errors.New("console is closed")
	}
	_, err := conn.Write(p)
	return err
}

func (s *consoleSession) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	conn := s.conn
	s.conn = nil
	s.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

type consoleManager struct {
	mu       sync.Mutex
	sessions map[string]*consoleSession
	byUID    map[string]string
}

func newConsoleManager() *consoleManager {
	return &consoleManager{sessions: map[string]*consoleSession{}, byUID: map[string]string{}}
}

func (m *consoleManager) add(id, uid string, conn net.Conn) {
	s := &consoleSession{uid: uid, conn: conn}
	var old *consoleSession
	m.mu.Lock()
	if existingID, ok := m.byUID[uid]; ok {
		old = m.sessions[existingID]
		delete(m.sessions, existingID)
	}
	m.sessions[id] = s
	m.byUID[uid] = id
	m.mu.Unlock()
	if old != nil {
		old.close()
	}

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				s.append(buf[:n])
			}
			if err != nil {
				s.close()
				m.remove(id)
				return
			}
		}
	}()
}

func (m *consoleManager) get(id string) *consoleSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id]
}

func (m *consoleManager) remove(id string) {
	m.mu.Lock()
	s := m.sessions[id]
	if s != nil {
		if current, ok := m.byUID[s.uid]; ok && current == id {
			delete(m.byUID, s.uid)
		}
	}
	m.mu.Unlock()
	if s != nil {
		s.close()
	}
}

func (m *consoleManager) closeAll() {
	m.mu.Lock()
	all := make([]*consoleSession, 0, len(m.sessions))
	for id, s := range m.sessions {
		delete(m.sessions, id)
		all = append(all, s)
	}
	for uid := range m.byUID {
		delete(m.byUID, uid)
	}
	m.mu.Unlock()
	for _, s := range all {
		s.close()
	}
}
