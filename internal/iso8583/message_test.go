package iso8583

import (
	"strings"
	"testing"
)

func TestParseMessageRejectsInvalidLengths(t *testing.T) {
	for _, tc := range []struct {
		name  string
		bit   int
		value string
	}{
		{"negative LLVAR", 2, "-1"},
		{"signed LLVAR", 2, "+1X"},
		{"oversize LLVAR", 2, "20" + strings.Repeat("1", 20)},
		{"negative LLLVAR", 48, "-01"},
		{"signed LLLVAR", 48, "+00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("malformed input caused a panic: %v", r)
				}
			}()
			var bitmap Bitmap
			bitmap.Set(1, true)
			bitmap.Set(tc.bit, true)
			raw := append([]byte("1240"), bitmap.Bytes()...)
			raw = append(raw, tc.value...)
			if _, err := ParseMessage(raw, EncodingASCII); err == nil {
				t.Fatal("expected a parse error")
			}
		})
	}
}

func TestParsePDSRejectsMalformedHeaders(t *testing.T) {
	for _, data := range []string{"0001-01", "0001+00", "ABCD000", "-001000"} {
		t.Run(data, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("malformed input caused a panic: %v", r)
				}
			}()
			if _, err := ParsePDS(data); err == nil {
				t.Fatal("expected a parse error")
			}
		})
	}
}
