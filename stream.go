package openflow

import (
	"bytes"
	"encoding/binary"
	"github.com/maufl/openflow/openflowxx"
	"log"
	"net"
)

type BufferPool struct {
	Empty chan *bytes.Buffer
	Full  chan *bytes.Buffer
}

func NewBufferPool() *BufferPool {
	m := new(BufferPool)
	m.Empty = make(chan *bytes.Buffer, 50)
	m.Full = make(chan *bytes.Buffer, 50)

	for i := 0; i < 50; i++ {
		m.Empty <- bytes.NewBuffer(make([]byte, 0, 2048))
	}
	return m
}

type MessageStream struct {
	conn net.Conn
	pool *BufferPool
	// OpenFlow Version
	Version uint8
	// Channel on which to publish connection errors
	Error chan error
	// Channel on which to publish inbound messages
	Inbound chan openflowxx.Message
	// Channel on which to receive outbound messages
	Outbound chan openflowxx.Message
	// Channel on which to receive a shutdown command
	Shutdown chan bool
}

// Returns a pointer to a new MessageStream. Used to parse
// OpenFlow messages from conn.
func NewMessageStream(conn net.Conn) *MessageStream {
	m := &MessageStream{
		conn,
		NewBufferPool(),
		0,
		make(chan error, 1),              // Error
		make(chan openflowxx.Message, 1), // Inbound
		make(chan openflowxx.Message, 1), // Outbound
		make(chan bool, 1),               // Shutdown
	}

	go m.outbound()
	go m.inbound()

	for i := 0; i < 1; i++ {
		go m.parse()
	}
	return m
}

func (m *MessageStream) GetAddr() net.Addr {
	return m.conn.RemoteAddr()
}

// Listen for a Shutdown signal or Outbound messages.
func (m *MessageStream) outbound() {
	for {
		select {
		case <-m.Shutdown:
			log.Println("Closing OpenFlow message stream.")
			close(m.Inbound)
			m.conn.Close()
			return
		case msg := <-m.Outbound:
			// Forward outbound messages to conn
			data, _ := msg.MarshalBinary()
			if _, err := m.conn.Write(data); err != nil {
				log.Println("OutboundError:", err)
				m.Error <- err
				m.Shutdown <- true
			}
		}
	}
}

func (m *MessageStream) inbound() {
	msg := 0
	hdr := 0
	hdrBuf := make([]byte, 4)

	tmp := make([]byte, 2048)
	buf := <-m.pool.Empty
	for {
		n, err := m.conn.Read(tmp)
		if err != nil {
			log.Println("InboundError", err)
			m.Error <- err
			m.Shutdown <- true
			return
		}

		for i := 0; i < n; i++ {
			if hdr < 4 {
				hdrBuf[hdr] = tmp[i]
				buf.WriteByte(tmp[i])
				hdr += 1
				if hdr >= 4 {
					msg = int(binary.BigEndian.Uint16(hdrBuf[2:])) - 4
				}
				continue
			}
			if msg > 0 {
				buf.WriteByte(tmp[i])
				msg = msg - 1
				if msg == 0 {
					hdr = 0
					m.pool.Full <- buf
					buf = <-m.pool.Empty
				}
				continue
			}
		}
	}
}

func (m *MessageStream) parse() {
	defer func() {
		recover()
	}()
	for {
		b := <-m.pool.Full
		msg, err := Parse(b.Bytes())
		// Log all message parsing errors.
		if err != nil {
			log.Print(err)
		} else {
			m.Inbound <- msg
		}
		if msg == nil && err == nil {
			panic("Contract violation")
		}
		// TODO we close the channel in inbound, this leads to a panic
		b.Reset()
		m.pool.Empty <- b
	}
}
