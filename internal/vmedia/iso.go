package vmedia

import (
	"fmt"
	"io"
	"os"
	"sync"
)

const sectorSize = 2048

type ISO struct {
	path string
	size int64

	// mu keeps Close from pulling the file descriptor out from under a SCSI
	// read: those run on separate goroutines.
	mu   sync.RWMutex
	file *os.File
}

func OpenISO(path string) (*ISO, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open ISO %q: %w", path, err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("stat ISO %q: %w", path, err)
	}
	if !st.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("ISO path %q is not a regular file", path)
	}
	if st.Size() <= 0 {
		_ = f.Close()
		return nil, fmt.Errorf("ISO path %q is empty", path)
	}
	return &ISO{path: path, file: f, size: st.Size()}, nil
}

func (i *ISO) Path() string {
	if i == nil {
		return ""
	}
	return i.path
}

func (i *ISO) Size() int64 {
	if i == nil {
		return 0
	}
	return i.size
}

func (i *ISO) ReadAt(offset int64, length int) ([]byte, error) {
	if i == nil {
		return nil, fmt.Errorf("ISO is closed")
	}
	if offset < 0 || length < 0 || offset+int64(length) > i.size {
		return nil, fmt.Errorf("ISO read out of range offset=%d length=%d size=%d", offset, length, i.size)
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.file == nil {
		return nil, fmt.Errorf("ISO is closed")
	}
	buf := make([]byte, length)
	_, err := i.file.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return buf, nil
}

func (i *ISO) Close() error {
	if i == nil {
		return nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.file == nil {
		return nil
	}
	err := i.file.Close()
	i.file = nil
	return err
}
