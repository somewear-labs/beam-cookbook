package main

import (
	"testing"
	"unicode/utf8"
)

func TestTruncateResponsePreservesValidUTF8(t *testing.T) {
	input := []byte("Connected Devices\n┌───┐")
	limit := len(input) - 1

	got, truncated := truncateResponse(input, limit)

	if !truncated {
		t.Fatal("truncateResponse reported that the response was not truncated")
	}
	if !utf8.Valid(got) {
		t.Fatalf("truncateResponse returned invalid UTF-8: %x", got)
	}
	if len(got) > limit {
		t.Fatalf("truncateResponse returned %d bytes with a %d-byte limit", len(got), limit)
	}
}

func TestTruncateResponseLeavesShortResponseUnchanged(t *testing.T) {
	input := []byte("Device is already connected")

	got, truncated := truncateResponse(input, 128)

	if truncated {
		t.Fatal("truncateResponse reported truncation for a short response")
	}
	if string(got) != string(input) {
		t.Fatalf("truncateResponse returned %q, want %q", got, input)
	}
}
