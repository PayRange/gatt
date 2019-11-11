package gatt

import (
	"errors"
	"fmt"
)

var (
	ErrNotBRSP = errors.New("Peripheral does not implement BRSP")
	ErrTimeout = errors.New("BRSP timeout")
	ErrClosed  = errors.New("BRSP was closed")

	brspService = MustParseUUID("DA2B84F1-6279-48DE-BDC0-AFBEA0226079")
	brspMode    = MustParseUUID("A87988B9-694C-479C-900E-95DFA6C00A24")
	brspRx      = MustParseUUID("BF03260C-7205-4C25-AF43-93B1C299D159")
	brspTx      = MustParseUUID("18CDA784-4BD3-4370-85BB-BFED91EC86AF")
)

type BRSP struct {
	p            Peripheral
	readReq      chan brspRequest
	writeReq     chan []byte
	flushReq     chan chan error
	incomingData chan brspIncoming
	outgoingData chan brspOutgoing
	writeErrors  chan error
	closed       chan struct{}
	brspService  *Service
	brspMode     *Characteristic
	brspRx       *Characteristic
	brspTx       *Characteristic
	inQueue      brspQueue
	outQueue     brspQueue
	txMode       bool
	outData      brspOutgoing
	readReqs     []brspRequest
	flushReqs    []chan error
	readError    error
	writeError   error
}

func (b *BRSP) Close() error {
	close(b.closed)

	return nil
}

func (b *BRSP) Flush() error {
	c := make(chan error)
	b.flushReq <- c
	err := <-c

	return err
}

func (b *BRSP) Read(p []byte) (int, error) {
	req := brspRequest{
		p: p,
		r: make(chan brspResult),
	}
	b.readReq <- req
	res := <-req.r

	return res.n, res.err
}

func (b *BRSP) Write(p []byte) (int, error) {
	b.writeReq <- p

	return len(p), nil
}

func (b *BRSP) discover() error {
	svcs, err := b.p.DiscoverServices([]UUID{brspService})
	if err != nil {
		return err
	}

	for _, s := range svcs {
		if s.UUID().Equal(brspService) {
			b.brspService = s
			break
		}
	}
	if b.brspService == nil {
		return ErrNotBRSP
	}

	chars, err := b.p.DiscoverCharacteristics([]UUID{brspMode, brspRx, brspTx}, b.brspService)
	if err != nil {
		return err
	}

	for _, c := range chars {
		u := c.UUID()
		if u.Equal(brspMode) {
			b.brspMode = c
		} else if u.Equal(brspRx) {
			b.brspRx = c
		} else if u.Equal(brspTx) {
			b.brspTx = c
		}
	}
	if b.brspMode == nil || b.brspRx == nil || b.brspTx == nil {
		return ErrNotBRSP
	}

	if _, err := b.p.DiscoverDescriptors(nil, b.brspTx); err != nil {
		return err
	}

	return nil
}

func (b *BRSP) handleFlushReq(c chan error) {
	if b.txMode {
		b.flushReqs = append(b.flushReqs, c)
	} else {
		c <- b.writeError
		b.writeError = nil
	}
}

func (b *BRSP) handleIncomingData(i brspIncoming) {
	if len(b.readReqs) > 0 {
		rr := b.readReqs[0]
		copy(b.readReqs, b.readReqs[1:])
		b.readReqs = b.readReqs[:len(b.readReqs)-1]
		n := copy(rr.p, i.data[:i.n])
		if i.n > n {
			b.inQueue.write(i.data[n:i.n])
		}
		rr.r <- brspResult{
			n:   n,
			err: i.err,
		}
	} else {
		b.inQueue.write(i.data[:i.n])
		if i.err != nil {
			b.readError = i.err
		}
	}
}

func (b *BRSP) handleOutgoingData() {
	n := b.outQueue.read(b.outData.data[:])
	if n > 0 {
		b.outData.n = n
	} else if b.outData.n > 0 {
		b.outData.n = 0
	} else {
		b.txMode = false
		for _, c := range b.flushReqs {
			c <- b.writeError
		}
		b.writeError = nil
	}
}

func (b *BRSP) handleReadReq(r brspRequest) {
	if b.inQueue.queued() > 0 {
		n := b.inQueue.read(r.p)
		r.r <- brspResult{
			n:   n,
			err: b.readError,
		}
		b.readError = nil
	} else {
		b.readReqs = append(b.readReqs, r)
	}
}

func (b *BRSP) handleWriteError(e error) {
	b.writeError = e
}

func (b *BRSP) handleWriteReq(p []byte) {
	if !b.txMode {
		l := len(p)
		if l > 20 {
			l = 20
		}
		copy(b.outData.data[:], p)
		b.outData.n = l
		b.txMode = true
		p = p[l:]
	}

	b.outQueue.write(p)
}

func (b *BRSP) init() error {
	if err := b.discover(); err != nil {
		return err
	}

	if err := b.p.SetIndicateValue(b.brspTx, nil); err != nil {
		return err
	}

	onTx := func(c *Characteristic, data []byte, err error) {
		fmt.Printf("brspTx %v: % x\n", err, data)
		bi := brspIncoming{err: err}
		bi.n = copy(bi.data[:], data)
		b.incomingData <- bi
	}

	if err := b.p.SetIndicateValue(b.brspTx, onTx); err != nil {
		return err
	}

	if err := b.p.WriteCharacteristic(b.brspMode, []byte{1}, true); err != nil {
		return err
	}

	return nil
}

func (b *BRSP) loop() {
	defer func() {
		for _, c := range b.flushReqs {
			c <- ErrClosed
		}

		for _, r := range b.readReqs {
			r.r <- brspResult{
				err: ErrClosed,
			}
		}
	}()

	for {
		if b.txMode {
			select {
			case r := <-b.readReq:
				b.handleReadReq(r)
			case w := <-b.writeReq:
				b.handleWriteReq(w)
			case f := <-b.flushReq:
				b.handleFlushReq(f)
			case d := <-b.incomingData:
				b.handleIncomingData(d)
			case b.outgoingData <- b.outData:
				b.handleOutgoingData()
			case e := <-b.writeErrors:
				b.handleWriteError(e)
			case <-b.closed:
				return
			}
		} else {
			select {
			case r := <-b.readReq:
				b.handleReadReq(r)
			case w := <-b.writeReq:
				b.handleWriteReq(w)
			case f := <-b.flushReq:
				b.handleFlushReq(f)
			case d := <-b.incomingData:
				b.handleIncomingData(d)
			case e := <-b.writeErrors:
				b.handleWriteError(e)
			case <-b.closed:
				return
			}
		}
	}
}

func (b *BRSP) writer() {
	for {
		select {
		case d := <-b.outgoingData:
			if d.n > 0 {
				fmt.Printf("brspRx % x (%s)\n", d.data[:d.n], string(d.data[:d.n]))
				if err := b.p.WriteCharacteristic(b.brspRx, d.data[:d.n], true); err != nil {
					b.writeErrors <- err
				}
			}
		case <-b.closed:
			return
		}
	}
}

func OpenBRSP(p Peripheral) (*BRSP, error) {
	b := &BRSP{
		p:            p,
		readReq:      make(chan brspRequest),
		writeReq:     make(chan []byte),
		flushReq:     make(chan chan error),
		incomingData: make(chan brspIncoming),
		outgoingData: make(chan brspOutgoing),
		writeErrors:  make(chan error),
		closed:       make(chan struct{}),
	}

	if err := b.init(); err != nil {
		return nil, err
	}

	go b.loop()
	go b.writer()

	return b, nil
}

type brspIncoming struct {
	data [20]byte
	n    int
	err  error
}

type brspOutgoing struct {
	data [20]byte
	n    int
}

type brspResult struct {
	n   int
	err error
}

type brspRequest struct {
	p []byte
	r chan brspResult
}

type brspQueue struct {
	data []byte
	head int
	tail int
}

func (q *brspQueue) queued() int {
	if q.head >= q.tail {
		return q.head - q.tail
	} else {
		return len(q.data) + q.head - q.tail
	}
}

func (q *brspQueue) read(p []byte) int {
	var n int

	if q.head >= q.tail {
		n = copy(p, q.data[q.tail:q.head])
		q.tail += n
	} else {
		n = copy(p, q.data[q.tail:])
		q.tail += n
		if q.tail == len(q.data) {
			m := copy(p[n:], q.data[:q.head])
			q.tail = m
			n += m
		}
	}

	if q.tail == q.head {
		q.head = 0
		q.tail = 0
	}

	return n
}

func (q *brspQueue) write(p []byte) {
	space := len(q.data) - q.queued()

	if len(p) >= space {
		need := len(p) - space + 1
		if need < 256 {
			need = 256
		}

		data := make([]byte, len(q.data)+need)
		n := q.read(data)
		q.data = data
		q.head = n
		q.tail = 0
	}

	n := len(q.data) - q.head
	if n < len(p) {
		copy(q.data[q.head:], p)
		p = p[n:]
		q.head = 0
	}

	copy(q.data[q.head:], p)
	q.head += len(p)
}
