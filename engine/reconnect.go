package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"wayline/native"
)

type permanentError struct{ error }

func (e permanentError) Unwrap() error { return e.error }

func retryableSessionError(err error) bool {
	var permanent permanentError
	var certificate *native.CertificateRejected
	var mfa *native.UnsupportedMFA
	var httpError *native.HTTPError
	if errors.As(err, &permanent) || errors.As(err, &certificate) || errors.As(err, &mfa) ||
		errors.Is(err, native.ErrAuthentication) || errors.Is(err, native.ErrTunnelDenied) {
		return false
	}
	if errors.As(err, &httpError) && httpError.Status >= 400 && httpError.Status < 500 {
		return false
	}
	return !errors.Is(err, context.Canceled)
}

func runSession(ctx context.Context, config Config) error {
	if err := config.validate(); err != nil {
		return permanentError{err}
	}
	delay := 2 * time.Second
	for {
		started := time.Now()
		err := run(ctx, config)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil || !config.AutoReconnect || !retryableSessionError(err) {
			return err
		}
		if time.Since(started) >= time.Minute {
			delay = 2 * time.Second
		}
		emit("retry", fmt.Sprintf("Connection lost (%v). Reconnecting", err), map[string]any{"retryInSeconds": int(delay.Seconds())})
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if delay < 30*time.Second {
			delay = min(delay*2, 30*time.Second)
		}
	}
}
