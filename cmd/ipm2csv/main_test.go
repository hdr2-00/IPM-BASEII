package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"mciipm/internal/ebcdic"
	"mciipm/internal/iso8583"
	"mciipm/internal/vbs"
)

func TestResolveEncodingAuto(t *testing.T) {
	for _, enc := range []iso8583.Encoding{iso8583.EncodingASCII, iso8583.EncodingEBCDIC} {
		t.Run(string(enc), func(t *testing.T) {
			mti := []byte("1240")
			if enc == iso8583.EncodingEBCDIC {
				mti = ebcdic.Encode(mti)
			}
			raw := append(mti, make([]byte, 16)...)
			var buf bytes.Buffer
			writer := vbs.NewWriter(&buf)
			if err := writer.Write(raw); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "input.ipm")
			if err := os.WriteFile(path, buf.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := resolveEncoding(path, "auto", false)
			if err != nil || got != enc {
				t.Fatalf("resolveEncoding = %q, %v; want %q", got, err, enc)
			}
		})
	}
}
