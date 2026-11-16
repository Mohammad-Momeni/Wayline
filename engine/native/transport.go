package native

import (
	"context"
	"encoding/binary"
	"net"
	"time"

	"wayline/ppp"
)

// frameTransport adapts gateway frames to PPP with one TLS writer and bounded queues.
// Control frames take priority over queued data; an in-flight write has a deadline.
// PPP owns the sole reader/demultiplexer and the bounded inbound IPv4 queue.
type frameTransport struct {
	conn          net.Conn
	ctx           context.Context
	cancel        context.CancelFunc
	control, data chan frameWrite
	done          chan struct{}
}

// frameWrite owns one bounded PPP payload and a one-slot write acknowledgement.
type frameWrite struct {
	ctx    context.Context
	packet []byte
	result chan error
}

// newFrameTransport starts the sole writer; its caller must join it through close.
func newFrameTransport(ctx context.Context, conn net.Conn) *frameTransport {
	ctx, cancel := context.WithCancel(ctx)
	f := &frameTransport{conn: conn, ctx: ctx, cancel: cancel, control: make(chan frameWrite, 8), data: make(chan frameWrite, 32), done: make(chan struct{})}
	go f.writeFrames()
	return f
}

// close cancels and joins the sole writer without closing the gateway connection.
func (f *frameTransport) close() { f.cancel(); <-f.done }

// ReadPacket reads a bounded complete PPP frame, interrupting partial reads on cancel.
// PPP validates the protocol and negotiated MRU before delivering IPv4 payloads.
func (f *frameTransport) ReadPacket(ctx context.Context) ([]byte, error) {
	finish, err := frameDeadline(ctx, f.conn.SetReadDeadline)
	if err != nil {
		return nil, err
	}
	packet, err := ReadFrame(f.conn)
	finish()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return packet, err
}

// WritePacket queues one bounded payload and waits for actual TLS write completion.
// Control and data admission have separate limits and honor caller cancellation.
func (f *frameTransport) WritePacket(ctx context.Context, packet []byte) error {
	if len(packet) < 2 || len(packet) > ppp.DefaultMRU+2 {
		return ErrInvalidFrame
	}
	request := frameWrite{ctx: ctx, packet: append([]byte(nil), packet...), result: make(chan error, 1)}
	queue := f.control
	if binary.BigEndian.Uint16(packet) == ppp.ProtocolIPv4 {
		queue = f.data
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.ctx.Done():
		return f.ctx.Err()
	case queue <- request:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.ctx.Done():
		return f.ctx.Err()
	case err := <-request.result:
		return err
	}
}

// writeFrames gives queued control priority and caps every write to fifteen seconds.
// Any incomplete frame poisons the stream and stops admission instead of retrying.
func (f *frameTransport) writeFrames() {
	defer close(f.done)
	var pending *frameWrite
	for {
		if f.ctx.Err() != nil {
			return
		}
		var request frameWrite
		select {
		case request = <-f.control:
		default:
			if pending != nil {
				request = *pending
				pending = nil
			} else {
				select {
				case <-f.ctx.Done():
					return
				case request = <-f.control:
				case request = <-f.data:
					// Retain one selected data frame locally rather than requeueing it.
					select {
					case control := <-f.control:
						data := request
						pending = &data
						request = control
					default:
					}
				}
			}
		}
		if err := request.ctx.Err(); err != nil {
			request.result <- err
			continue
		}
		ctx, cancel := context.WithTimeout(request.ctx, 15*time.Second)
		stop := context.AfterFunc(f.ctx, cancel)
		finish, err := frameDeadline(ctx, f.conn.SetWriteDeadline)
		if err == nil {
			err = WriteFrame(f.conn, request.packet)
			finish()
		}
		stop()
		cancel()
		request.result <- err
		if err != nil {
			f.cancel()
			_ = f.conn.SetReadDeadline(time.Now())
			return
		}
	}
}

// frameDeadline joins cancellation before clearing a directional I/O deadline.
// The caller serializes operations in that direction and always invokes the cleanup.
func frameDeadline(ctx context.Context, set func(time.Time) error) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	deadline, _ := ctx.Deadline()
	if err := set(deadline); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = set(time.Now()); close(done) })
	return func() {
		if !stop() {
			<-done
		}
		_ = set(time.Time{})
	}, nil
}

// Transport exposes bounded, serialized PPP framing to the userspace engine.
func NewTransport(ctx context.Context, conn net.Conn) *frameTransport {
	return newFrameTransport(ctx, conn)
}
func (f *frameTransport) Close() { f.close() }
