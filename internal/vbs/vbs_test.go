package vbs

import (
	"bytes"
	"io"
	"testing"
)

func TestUnblock1014ReadsOnDemand(t *testing.T) {
	input := bytes.NewReader(bytes.Repeat([]byte{'X'}, blockTotalSize*2))
	r, err := Unblock1014(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(r, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	if consumed := input.Size() - int64(input.Len()); consumed != 1 {
		t.Fatalf("reader consumed %d bytes for a one-byte read", consumed)
	}
}

func TestUnblock1014Boundaries(t *testing.T) {
	for _, size := range []int{0, 1, 1012, 1013, 1014, 2028, 2030} {
		input := bytes.Repeat([]byte{'X'}, size)
		r, err := Unblock1014(bytes.NewReader(input))
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		want := size / 1014 * 1012
		remaining := size % 1014
		if remaining > 1012 {
			remaining = 1012
		}
		want += remaining
		if len(got) != want {
			t.Errorf("input %d bytes: got %d, want %d", size, len(got), want)
		}
	}
}
