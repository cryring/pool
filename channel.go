package pool

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// channelPool implements the Pool interface based on buffered channels.
type channelPool struct {
	// storage for our net.Conn connections
	mu    sync.RWMutex
	conns chan *Conn

	// net.Conn generator
	factory Factory
}

// Factory is a function to create new connections.
type Factory func() (net.Conn, error)

// NewChannelPool returns a new pool based on buffered channels with an initial
// capacity and maximum capacity. Factory is used when initial capacity is
// greater than zero to fill the pool. A zero initialCap doesn't fill the Pool
// until a new Get() is called. During a Get(), If there is no new connection
// available in the pool, a new connection will be created via the Factory()
// method.
func NewChannelPool(initialCap, maxCap int, factory Factory) (Pool, error) {
	if initialCap < 0 || maxCap <= 0 || initialCap > maxCap {
		return nil, errors.New("invalid capacity settings")
	}

	c := &channelPool{
		conns:   make(chan *Conn, maxCap),
		factory: factory,
	}

	// create initial connections, if something goes wrong,
	// just close the pool error out.
	for i := 0; i < initialCap; i++ {
		conn, err := factory()
		if err != nil {
			c.Close()
			return nil, fmt.Errorf("factory is not able to fill the pool: %s", err)
		}
		c.conns <- &Conn{c: c, Conn: conn}
	}

	return c, nil
}

func (c *channelPool) getConnsAndFactory() (chan *Conn, Factory) {
	c.mu.RLock()
	conns := c.conns
	factory := c.factory
	c.mu.RUnlock()
	return conns, factory
}

// Get implements the Pool interfaces Get() method. If there is no new
// connection available in the pool, a new connection will be created via the
// Factory() method.
func (c *channelPool) Get() (net.Conn, error) {
	conns, factory := c.getConnsAndFactory()
	if conns == nil {
		return nil, ErrClosed
	}

	// wrap our connections with out custom net.Conn implementation (wrapConn
	// method) that puts the connection back to the pool if it's closed.
	for {
		select {
		case pc := <-conns:
			if pc == nil {
				return nil, ErrClosed
			}
			// If this connection has a ReadTimeout which we've been setting on
			// reads, reset it to its default value before we attempt a non-blocking
			// read, otherwise the scheduler will just time us out before we can read
			err := pc.SetReadDeadline(time.Time{})
			if err == nil {
				err = pc.connCheck()
			}
			if err != nil {
				// Closing bad idle connection
				pc.Conn.Close()
				continue
			}
			return pc, nil
		default:
			conn, err := factory()
			if err != nil {
				return nil, err
			}
			return &Conn{c: c, Conn: conn}, nil
		}
	}

}

// put puts the connection back to the pool. If the pool is full or closed,
// conn is simply closed. A nil conn will be rejected.
func (c *channelPool) put(conn *Conn) error {
	if conn == nil {
		return errors.New("connection is nil. rejecting")
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.conns == nil {
		// pool is closed, close passed connection
		return conn.Conn.Close()
	}

	// put the resource back into the pool. If the pool is full, this will
	// block and the default case will be executed.
	select {
	case c.conns <- conn:
		return nil
	default:
		// pool is full, close passed connection
		return conn.Conn.Close()
	}
}

func (c *channelPool) Close() {
	c.mu.Lock()
	conns := c.conns
	c.conns = nil
	c.factory = nil
	c.mu.Unlock()

	if conns == nil {
		return
	}

	close(conns)
	for conn := range conns {
		conn.Close()
	}
}

func (c *channelPool) Len() int {
	conns, _ := c.getConnsAndFactory()
	return len(conns)
}
