package vmedia

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

type Conn interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
}

type Logger func(format string, args ...any)

type Session struct {
	conn Conn
	iso  *ISO
	logf Logger

	stopOnce sync.Once
	writeMu  sync.Mutex
	done     chan struct{}
}

func Start(ctx context.Context, conn Conn, iso *ISO, logf Logger) *Session {
	s := &Session{
		conn: conn,
		iso:  iso,
		logf: logf,
		done: make(chan struct{}),
	}
	go s.run(ctx)
	go s.keepalive(ctx)
	return s
}

func (s *Session) Done() <-chan struct{} {
	return s.done
}

func (s *Session) Close() error {
	var err error
	s.stopOnce.Do(func() {
		if s.conn != nil {
			err = s.conn.Close()
		}
		if closeErr := s.iso.Close(); err == nil {
			err = closeErr
		}
	})
	return err
}

func (s *Session) log(format string, args ...any) {
	if s.logf != nil {
		s.logf(format, args...)
	}
}

func (s *Session) run(ctx context.Context) {
	defer close(s.done)
	defer func() {
		if err := s.Close(); err != nil {
			s.log("vmedia close error: %v", err)
		}
	}()

	cd := newCDImage(s.conn, s.iso, &s.writeMu, s.logf)
	s.log("vmedia scsi loop start iso=%q size=%d", s.iso.Path(), s.iso.Size())
	for {
		select {
		case <-ctx.Done():
			s.log("vmedia scsi loop stop: context done")
			return
		default:
		}
		keepGoing, err := cd.process()
		if err != nil {
			if errors.Is(err, io.EOF) {
				s.log("vmedia scsi loop stop: eof")
			} else {
				s.log("vmedia scsi loop stop: %v", err)
			}
			return
		}
		if !keepGoing {
			s.log("vmedia scsi loop stop: media ejected")
			return
		}
	}
}

func (s *Session) keepalive(ctx context.Context) {
	header := KeepaliveHeader().Bytes()
	if err := s.write(header[:]); err != nil {
		s.log("vmedia keepalive initial error: %v", err)
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.done:
			return
		case <-ticker.C:
			if err := s.write(header[:]); err != nil {
				s.log("vmedia keepalive error: %v", err)
				return
			}
		}
	}
}

func (s *Session) write(data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.conn.Write(data)
	return err
}

type cdImage struct {
	conn       Conn
	iso        *ISO
	writeMu    *sync.Mutex
	logf       Logger
	mediaState int
	eventState int
	media      byte
}

func newCDImage(conn Conn, iso *ISO, writeMu *sync.Mutex, logf Logger) *cdImage {
	if writeMu == nil {
		writeMu = &sync.Mutex{}
	}
	return &cdImage{conn: conn, iso: iso, writeMu: writeMu, logf: logf}
}

func (c *cdImage) log(format string, args ...any) {
	if c.logf != nil {
		c.logf(format, args...)
	}
}

func (c *cdImage) process() (bool, error) {
	var req [12]byte
	if _, err := io.ReadFull(c.conn, req[:]); err != nil {
		return false, err
	}
	if req[0] == 0xfe {
		h := SyncHeader(req[4:])
		_, err := c.conn.Write(h[:])
		return true, err
	}

	c.updateMediaState()
	c.log("vmedia scsi cmd=0x%02x cdb=%x media_state=%d event_state=%d", req[0], req, c.mediaState, c.eventState)
	switch req[0] {
	case 0x1e:
		return true, c.preventAllowRemoval()
	case 0x25:
		return true, c.readCapacity()
	case 0x1d:
		return true, nil
	case 0x00:
		return true, c.testUnitReady()
	case 0x28, 0xa8:
		return true, c.read(req[:])
	case 0x1b:
		return c.startStopUnit(req[:])
	case 0x43:
		return true, c.readTOC(req[:])
	case 0x5a:
		return true, c.modeSense()
	case 0x4a:
		return true, c.getEventStatus(req[:])
	default:
		c.log("vmedia scsi unknown cmd=0x%02x", req[0])
		return true, c.sendSense(5, 36, 0, nil)
	}
}

func (c *cdImage) updateMediaState() {
	if c.iso.Size() <= 0 {
		c.media = 0
		c.mediaState = 0
		c.eventState = 0
		return
	}
	c.media = 1
	if c.mediaState < 2 {
		c.mediaState++
	}
	if c.eventState == 4 {
		c.eventState = 0
	}
	if c.eventState < 2 {
		c.eventState++
	}
}

func (c *cdImage) preventAllowRemoval() error {
	return c.sendSense(0, 0, 0, nil)
}

func (c *cdImage) readCapacity() error {
	if c.mediaState == 0 {
		return c.sendSense(2, 58, 0, nil)
	}
	if c.mediaState == 1 {
		return c.sendSense(6, 40, 0, nil)
	}
	blocks := c.iso.Size() / sectorSize
	if blocks <= 0 {
		return c.sendSense(2, 58, 0, nil)
	}
	data := make([]byte, 8)
	binary.BigEndian.PutUint32(data[0:4], uint32(blocks-1))
	binary.BigEndian.PutUint32(data[4:8], sectorSize)
	return c.sendSense(0, 0, 0, data)
}

func (c *cdImage) testUnitReady() error {
	if c.mediaState == 0 {
		return c.sendSense(2, 58, 0, nil)
	}
	if c.mediaState == 1 {
		c.mediaState = 2
		return c.sendSense(6, 40, 0, nil)
	}
	return c.sendSense(0, 0, 0, nil)
}

func (c *cdImage) read(cmd []byte) error {
	var blocks uint32
	if cmd[0] == 0xa8 {
		blocks = binary.BigEndian.Uint32(cmd[6:10])
	} else {
		blocks = uint32(binary.BigEndian.Uint16(cmd[7:9]))
	}
	lba := binary.BigEndian.Uint32(cmd[2:6])
	length := int64(blocks) * sectorSize
	offset := int64(lba) * sectorSize
	if length == 0 {
		return c.sendSense(0, 0, 0, nil)
	}
	if offset < 0 || offset+length > c.iso.Size() {
		c.log("vmedia read out of range lba=%d bytes=%d size=%d", lba, length, c.iso.Size())
		return c.sendSense(5, 33, 0, nil)
	}
	c.log("vmedia read lba=%d offset=%d bytes=%d", lba, offset, length)
	if length > 131072 {
		c.writeMu.Lock()
		defer c.writeMu.Unlock()
		if err := c.writeHeaderLocked(0, 0, 0, int(length)); err != nil {
			return err
		}
		for done := int64(0); done < length; {
			n := int64(131072)
			if remaining := length - done; remaining < n {
				n = remaining
			}
			data, err := c.iso.ReadAt(offset+done, int(n))
			if err != nil {
				return err
			}
			if _, err := c.conn.Write(data); err != nil {
				return err
			}
			done += n
		}
		return nil
	}
	data, err := c.iso.ReadAt(offset, int(length))
	if err != nil {
		return err
	}
	return c.sendSense(0, 0, 0, data)
}

func (c *cdImage) startStopUnit(cmd []byte) (bool, error) {
	if err := c.sendSense(0, 0, 0, nil); err != nil {
		return false, err
	}
	if cmd[4]&3 == 2 {
		c.mediaState = 0
		c.eventState = 4
		return false, nil
	}
	return true, nil
}

func (c *cdImage) readTOC(cmd []byte) error {
	msf := cmd[1]&2 != 0
	format := (cmd[9] & 0xc0) >> 6
	allocation := int(binary.BigEndian.Uint16(cmd[7:9]))
	if allocation <= 0 {
		return c.sendSense(0, 0, 0, nil)
	}
	length := allocation
	if length > 412 {
		length = 412
	}
	data := make([]byte, length)
	blocks := int(c.iso.Size() / sectorSize)
	totalSeconds := float64(blocks)/75.0 + 2.0
	minutes := int(totalSeconds) / 60
	seconds := int(totalSeconds) % 60
	frames := int((totalSeconds - float64(int(totalSeconds))) * 75.0)
	if format == 0 && len(data) >= 20 {
		data[1] = 18
		data[2] = 1
		data[3] = 1
		data[5] = 20
		data[6] = 1
		if msf {
			data[10] = 2
			data[17] = byte(minutes)
			data[18] = byte(seconds)
			data[19] = byte(frames)
		} else {
			data[17] = byte((blocks >> 16) & 0xff)
			data[18] = byte((blocks >> 8) & 0xff)
			data[19] = byte(blocks & 0xff)
		}
		data[13] = 20
		data[14] = 170
	}
	if format == 1 && len(data) >= 12 {
		data[1] = 10
		data[2] = 1
		data[3] = 1
		data[5] = 20
		data[6] = 1
		if msf {
			data[10] = 2
		}
	}
	return c.sendSense(0, 0, 0, data)
}

func (c *cdImage) modeSense() error {
	data := []byte{0, 8, 1, 0, 0, 0, 0, 0}
	c.media = data[2]
	return c.sendSense(0, 0, 0, data)
}

func (c *cdImage) getEventStatus(cmd []byte) error {
	if cmd[1]&1 == 0 {
		return c.sendSense(5, 36, 0, nil)
	}
	classes := cmd[4]
	allocation := int(binary.BigEndian.Uint16(cmd[7:9]))
	if allocation <= 0 {
		return c.sendSense(0, 0, 0, nil)
	}
	if classes&0x10 == 0 {
		data := []byte{0, 2, 128, 16}
		if allocation < len(data) {
			data = data[:allocation]
		}
		return c.sendSense(0, 0, 0, data)
	}
	data := []byte{0, 6, 4, 16, 0, 0, 0, 0}
	switch c.eventState {
	case 1:
		data[4] = 4
		data[5] = 2
		if allocation > 4 {
			c.eventState = 2
		}
	case 4:
		data[4] = 3
		if allocation > 4 {
			c.eventState = 0
		}
	default:
		data[5] = 2
	}
	if allocation < len(data) {
		data = data[:allocation]
	}
	return c.sendSense(0, 0, 0, data)
}

func (c *cdImage) sendSense(sense, asc, ascq byte, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if data == nil {
		return c.writeHeaderLocked(sense, asc, ascq, 0)
	}
	h := c.replyHeader(sense, asc, ascq, len(data)).Bytes()
	buf := make([]byte, 0, len(h)+len(data))
	buf = append(buf, h[:]...)
	buf = append(buf, data...)
	_, err := c.conn.Write(buf)
	return err
}

func (c *cdImage) writeHeaderLocked(sense, asc, ascq byte, length int) error {
	if length < 0 {
		return fmt.Errorf("negative vmedia reply length %d", length)
	}
	h := c.replyHeader(sense, asc, ascq, length).Bytes()
	_, err := c.conn.Write(h[:])
	return err
}

func (c *cdImage) replyHeader(sense, asc, ascq byte, length int) ReplyHeader {
	return ReplyHeader{Media: c.media}.WithSense(sense, asc, ascq, length)
}
