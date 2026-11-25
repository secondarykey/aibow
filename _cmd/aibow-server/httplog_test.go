package main

import "testing"

func TestIsTLSHandshakeError(t *testing.T) {
	for line, want := range map[string]bool{
		"http: TLS handshake error from 192.168.1.10:51234: remote error: tls: unknown certificate\n": true,
		"http: TLS handshake error from [::1]:51234: EOF\n":                                           true,
		"http: Accept error: accept tcp [::]:8080: too many open files; retrying in 5ms\n":            false,
		"http: panic serving 127.0.0.1:1234: boom\n":                                                  false,
	} {
		if got := isTLSHandshakeError([]byte(line)); got != want {
			t.Errorf("isTLSHandshakeError(%q) = %v, want %v", line, got, want)
		}
	}
}
