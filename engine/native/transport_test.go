package native

import (
	"context"
	"net"
	"testing"
	"time"
)

type writeDeadlineProbe struct {
	net.Conn
	deadlines chan time.Time
}

func (c writeDeadlineProbe) SetWriteDeadline(deadline time.Time) error {
	if !deadline.IsZero() {
		c.deadlines <- deadline
	}
	return c.Conn.SetWriteDeadline(deadline)
}

func TestFrameWriteAllowsMobileStallsAndHonorsShorterCallerDeadline(t *testing.T) {
	for _, callerLimit := range []time.Duration{0, time.Second} {
		left, right := net.Pipe()
		probe := writeDeadlineProbe{left, make(chan time.Time, 4)}
		transport := newFrameTransport(context.Background(), probe)
		ctx := context.Background()
		cancel := func() {}
		if callerLimit != 0 {
			ctx, cancel = context.WithTimeout(ctx, callerLimit)
		}
		result := make(chan error, 1)
		started := time.Now()
		go func() { result <- transport.WritePacket(ctx, []byte{0, 0x21, 0x45}) }()
		var deadline time.Time
		select {
		case deadline = <-probe.deadlines:
		case <-time.After(time.Second):
			t.Fatal("writer did not set a deadline")
		}
		limit := 15 * time.Second
		if callerLimit != 0 {
			limit = callerLimit
		}
		duration := deadline.Sub(started)
		if duration < limit-time.Second/4 || duration > limit+time.Second/4 {
			t.Errorf("write budget = %v, want %v", duration, limit)
		}
		if _, err := ReadFrame(right); err != nil {
			t.Error(err)
		}
		if err := <-result; err != nil {
			t.Error(err)
		}
		cancel()
		transport.close()
		left.Close()
		right.Close()
	}
}
