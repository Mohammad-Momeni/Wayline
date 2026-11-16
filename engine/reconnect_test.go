package main

import (
	"context"
	"errors"
	"io"
	"testing"

	"wayline/native"
)

func TestReconnectStopsOnAuthenticationCertificateAndConfigurationErrors(t *testing.T) {
	for _, err := range []error{
		native.ErrAuthentication, native.ErrTunnelDenied,
		&native.CertificateRejected{}, &native.UnsupportedMFA{},
		&native.HTTPError{Status: 403}, permanentError{errors.New("invalid Xray config")},
		context.Canceled,
	} {
		if retryableSessionError(err) {
			t.Fatalf("permanent failure was retryable: %T", err)
		}
	}
	for _, err := range []error{io.EOF, context.DeadlineExceeded, &native.HTTPError{Status: 503}} {
		if !retryableSessionError(err) {
			t.Fatalf("transient failure was not retryable: %T", err)
		}
	}
}
