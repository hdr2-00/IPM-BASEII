// Package baseii implements parsing of Visa BASE II Center Transaction
// Files (CTF) and Interchange Transaction Files (ITF), focused on the
// TC 05 Draft Data transaction family (TC 05/06/07/15/16/17/25/26/27/35/
// 36/37), per the "BASE II Clearing Interchange Formats, TC 01 to TC 49"
// manual (19 October 2019 edition).
//
// Records may be fixed-width or line-delimited. Line-delimited exports
// can contain short control records; draft records still require 168
// decoded bytes after the ITF hash is removed.
package baseii

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
)

// Format identifies which of the two record shapes a file uses.
type Format string

const (
	FormatCTF Format = "ctf" // 168 bytes/record
	FormatITF Format = "itf" // 170 bytes/record (168 + 2-byte hash total)
)

const (
	ctfRecordLength = 168
	itfRecordLength = 170
	itfHashLength   = 2
)

// RecordLength returns the fixed byte length for the given format.
func (f Format) RecordLength() int {
	if f == FormatITF {
		return itfRecordLength
	}
	return ctfRecordLength
}

// Framing identifies whether records are back-to-back with no delimiter
// (as the manual describes) or terminated by a line ending (as seen in
// real-world exports).
type Framing string

const (
	FramingFixed Framing = "fixed" // no delimiter; every record is exactly Format.RecordLength() bytes
	FramingLines Framing = "lines" // CRLF- or LF-terminated; record length varies line to line
)

// DetectFraming peeks at the start of a file to guess its framing: if a
// CR or LF byte appears anywhere within the first couple of records'
// worth of data, the file is treated as line-delimited. This scans a
// window rather than checking only the exact byte at
// format.RecordLength() - real files have shown that even the very first
// record isn't reliably that exact length (e.g. some file/batch control
// records use a shorter fixed length than TC05-family TCRs), so a
// single-offset check would miss line-delimited files that happen to
// start with a short record.
func DetectFraming(sample []byte, format Format) Framing {
	n := format.RecordLength() * 2
	if n > len(sample) {
		n = len(sample)
	}
	if bytes.ContainsAny(sample[:n], "\r\n") {
		return FramingLines
	}
	return FramingFixed
}

// Reader reads successive TCR records from a byte stream, in either
// framing. For ITF format, the 2-byte hash total is stripped so callers
// always see a 168-byte CTF-shaped record, keeping field position tables
// (documented in CTF terms) directly usable regardless of source format.
type Reader struct {
	br           *bufio.Reader
	format       Format
	framing      Framing
	RecordNumber int
}

// NewReader creates a TCR reader for the given record shape and framing.
func NewReader(r io.Reader, format Format, framing Framing) *Reader {
	return &Reader{br: bufio.NewReaderSize(r, 64*1024), format: format, framing: framing, RecordNumber: 1}
}

// Next reads the next TCR record. In FramingFixed, always returns exactly
// format.RecordLength() bytes (minus the ITF hash, if applicable). In
// FramingLines, returns whatever's on the next line (minus its \r\n or
// \n), whatever length that happens to be - callers (decodeTCR) already
// tolerate a record shorter than a field's declared position by simply
// not populating that field, so a short control record just yields fewer
// decoded fields rather than an error.
//
// Returns io.EOF cleanly at end of file. A read that produces some but
// not enough bytes for a complete fixed-length record is reported as a
// truncation error rather than silently misreading past it - mirroring
// the tolerant-but-honest approach used for the IPM/VBS reader.
func (r *Reader) Next() ([]byte, error) {
	if r.framing == FramingLines {
		return r.nextLine()
	}
	return r.nextFixed()
}

func (r *Reader) nextFixed() ([]byte, error) {
	want := r.format.RecordLength()
	buf := make([]byte, want)
	n, err := io.ReadFull(r.br, buf)
	if n == 0 {
		return nil, io.EOF
	}
	if err != nil {
		return nil, fmt.Errorf("baseii: record %d: truncated (wanted %d bytes, got %d): %w", r.RecordNumber, want, n, err)
	}
	r.RecordNumber++
	return r.stripITFHash(buf), nil
}

func (r *Reader) nextLine() ([]byte, error) {
	line, err := r.br.ReadBytes('\n')
	if len(line) == 0 && err != nil {
		return nil, io.EOF
	}
	line = bytes.TrimRight(line, "\r\n")
	if len(line) == 0 {
		// blank line (e.g. trailing newline at EOF) - skip it rather than
		// surfacing a zero-length "record".
		if err != nil {
			return nil, io.EOF
		}
		return r.nextLine()
	}
	r.RecordNumber++
	return r.stripITFHash(line), nil
}

func (r *Reader) stripITFHash(buf []byte) []byte {
	if r.format != FormatITF || len(buf) < 2+itfHashLength {
		return buf
	}
	out := make([]byte, 0, len(buf)-itfHashLength)
	out = append(out, buf[:2]...)               // positions 1-2, unaffected
	out = append(out, buf[2+itfHashLength:]...) // skip the 2-byte hash total, rest shifts back
	return out
}
