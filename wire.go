package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

// Das Wire-Protokoll aus docs/plugin-wire-protocol: Frame = len u32 | sid u32 | kind u8 | payload,
// alles big-endian. Nur das, was ein Modul braucht.

const (
	kindControl = 1
	kindData    = 2
	kindEnd     = 3
	kindReset   = 4
	kindCredit  = 5

	maxPayload = 1 << 20
)

type frame struct {
	sid     uint32
	kind    byte
	payload []byte
}

type channel struct {
	r  *bufio.Reader
	w  io.Writer
	mu sync.Mutex
}

func (c *channel) read() (frame, error) {
	var h [9]byte
	if _, err := io.ReadFull(c.r, h[:]); err != nil {
		return frame{}, err
	}
	n := binary.BigEndian.Uint32(h[0:4])
	if n > maxPayload {
		return frame{}, fmt.Errorf("payload %d over the cap", n)
	}
	f := frame{sid: binary.BigEndian.Uint32(h[4:8]), kind: h[8], payload: make([]byte, n)}
	_, err := io.ReadFull(c.r, f.payload)
	return f, err
}

func (c *channel) write(sid uint32, kind byte, p []byte) error {
	buf := make([]byte, 9+len(p))
	binary.BigEndian.PutUint32(buf[0:4], uint32(len(p)))
	binary.BigEndian.PutUint32(buf[4:8], sid)
	buf[8] = kind
	copy(buf[9:], p)
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.w.Write(buf)
	return err
}

func (c *channel) credit(sid, n uint32) error {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], n)
	return c.write(sid, kindCredit, b[:])
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcErr) Error() string { return fmt.Sprintf("rpc %d: %s", e.Code, e.Message) }

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

func logf(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	fmt.Fprintf(os.Stderr, "%s: %s\n", name, msg)
	dbg.write(msg)
}
