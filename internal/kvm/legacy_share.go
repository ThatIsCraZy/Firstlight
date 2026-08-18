package kvm

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
)

const legacyShareRecordLength = 10

type LegacyShareLeader struct {
	leader *Conn

	mu     sync.Mutex
	peers  map[*legacySharePeer]struct{}
	closed bool
}

type legacySharePeer struct {
	conn    net.Conn
	ip      net.IP
	writeMu sync.Mutex
}

func NewLegacyShareLeader(leader *Conn) *LegacyShareLeader {
	return &LegacyShareLeader{
		leader: leader,
		peers:  make(map[*legacySharePeer]struct{}),
	}
}

func (s *LegacyShareLeader) ConnectPeer(ctx context.Context, address string, port uint16) error {
	if s == nil || s.leader == nil {
		return fmt.Errorf("legacy shared-session leader is unavailable")
	}
	ip := net.ParseIP(address)
	if ip == nil {
		return fmt.Errorf("legacy shared-session peer address %q is not an IP address", address)
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(address, strconv.Itoa(int(port))))
	if err != nil {
		return err
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}
	if err := s.addPeerConn(conn, ip); err != nil {
		_ = conn.Close()
		return err
	}
	return nil
}

func (s *LegacyShareLeader) addPeerConn(conn net.Conn, ip net.IP) error {
	if conn == nil || ip == nil {
		return fmt.Errorf("legacy shared-session peer connection and IP are required")
	}
	peer := &legacySharePeer{conn: conn, ip: append(net.IP(nil), ip...)}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("legacy shared-session leader is closed")
	}
	s.peers[peer] = struct{}{}
	s.mu.Unlock()
	go s.readPeer(peer)
	return nil
}

func (s *LegacyShareLeader) Broadcast(data []byte) {
	if s == nil || len(data) == 0 {
		return
	}
	s.mu.Lock()
	peers := make([]*legacySharePeer, 0, len(s.peers))
	for peer := range s.peers {
		peers = append(peers, peer)
	}
	s.mu.Unlock()
	for _, peer := range peers {
		peer.writeMu.Lock()
		_ = peer.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		err := writeAll(peer.conn, data)
		_ = peer.conn.SetWriteDeadline(time.Time{})
		peer.writeMu.Unlock()
		if err != nil {
			s.removePeer(peer, true)
		}
	}
}

func (s *LegacyShareLeader) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	peers := make([]*legacySharePeer, 0, len(s.peers))
	for peer := range s.peers {
		peers = append(peers, peer)
		delete(s.peers, peer)
	}
	s.mu.Unlock()
	var firstErr error
	for _, peer := range peers {
		if err := peer.conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *LegacyShareLeader) PeerCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.peers)
}

func (s *LegacyShareLeader) readPeer(peer *legacySharePeer) {
	opened := false
	defer func() { s.removePeer(peer, opened) }()
	var record [legacyShareRecordLength]byte
	for {
		if _, err := io.ReadFull(peer.conn, record[:]); err != nil {
			return
		}
		switch record[0] {
		case 1, 2:
			if _, err := s.leader.Write(record[:]); err != nil {
				return
			}
		case 3:
			return
		case 4:
			if !opened {
				if _, err := s.leader.Write(LegacySharePeerEventRecord(peer.ip, true)); err != nil {
					return
				}
				opened = true
			}
		default:
			return
		}
	}
}

func (s *LegacyShareLeader) removePeer(peer *legacySharePeer, notify bool) {
	if s == nil || peer == nil {
		return
	}
	s.mu.Lock()
	_, exists := s.peers[peer]
	if exists {
		delete(s.peers, peer)
	}
	s.mu.Unlock()
	if !exists {
		return
	}
	_ = peer.conn.Close()
	if notify {
		_, _ = s.leader.Write(LegacySharePeerEventRecord(peer.ip, false))
	}
}

func LegacyShareOpeningRecord() []byte {
	return []byte{4, 0, 0, 0, 0, 0, 0, 0, 0, 0}
}

func LegacyShareClosingRecord() []byte {
	return []byte{3, 0, 0, 0, 0, 0, 0, 0, 0, 0}
}

func LegacySharePeerEventRecord(ip net.IP, opening bool) []byte {
	if ipv4 := ip.To4(); ipv4 != nil {
		command := byte(13)
		if opening {
			command = 14
		}
		out := make([]byte, 10)
		out[0] = command
		copy(out[2:6], ipv4)
		return out
	}
	command := byte(17)
	if opening {
		command = 16
	}
	out := make([]byte, 20)
	out[0] = command
	out[2] = 24
	copy(out[4:20], ip.To16())
	return out
}

func (c *Conn) SendLegacyShareDecision(accept bool) error {
	command := byte(3)
	if accept {
		command = 4
	}
	_, err := c.Write([]byte{command, 0, 0, 0})
	return err
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
