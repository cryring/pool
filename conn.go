package pool

import (
	"errors"
	"io"
	"net"
	"sync"
	"syscall"
)

// Conn is a wrapper around net.Conn to modify the the behavior of
// net.Conn's Close() method.
type Conn struct {
	net.Conn
	mu       sync.RWMutex
	c        *channelPool
	unusable bool
}

// Close puts the given connects back to the pool instead of closing it.
func (p *Conn) Close() error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.unusable {
		if p.Conn != nil {
			return p.Conn.Close()
		}
		return nil
	}
	return p.c.put(p)
}

// MarkUnusable marks the connection not usable any more, to let the pool close it instead of returning it to pool.
func (p *Conn) MarkUnusable() {
	p.mu.Lock()
	p.unusable = true
	p.mu.Unlock()
}

func (p *Conn) connCheck() error {
	var (
		n    int
		err  error
		buff [1]byte
	)

	sconn, ok := p.Conn.(syscall.Conn)
	if !ok {
		return nil
	}
	rc, err := sconn.SyscallConn()
	if err != nil {
		return err
	}
	rerr := rc.Read(func(fd uintptr) bool {
		n, err = syscall.Read(int(fd), buff[:])
		return true
	})
	switch {
	case rerr != nil:
		return rerr
	case n == 0 && err == nil:
		return io.EOF
	case n > 0:
		return errors.New("unexpected read")
	case err == syscall.EAGAIN || err == syscall.EWOULDBLOCK:
		return nil
	default:
		return err
	}
}
