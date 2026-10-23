// Package vbs implements readers and writers for the "VBS" (Variable Block
// Sequential) framing format used by Mastercard IPM clearing files.
//
// VBS format: each logical record is prefixed with a 4-byte big-endian
// length, with a terminating zero-length record marking end of file.
//
// Mainframe-originated files often add IBM 1014-byte block framing on top of
// VBS: the VBS byte stream is split into 1012-byte chunks, each followed by
// 2 filler bytes, with the very last block padded out to the full 1014
// bytes. The filler byte value is conventionally 0x40 (EBCDIC/ASCII space),
// but real-world files vary (0x00 is also common) - Unblock1014 does not
// validate the filler value, it just discards the last 2 bytes of every
// 1014-byte chunk unconditionally. This matches the behavior of the
// widely-used cardutil Python library, whose Unblock1014 class also never
// inspects the padding content.
package vbs

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	blockDataSize   = 1012
	blockTotalSize  = 1014
	writerPadChar   = 0x40      // standard EBCDIC/ASCII space; used only when producing new 1014-blocked output
	maxRecordLength = 6_000_000 // sanity ceiling; real IPM records are a few KB at most
)

// ErrBadBlocking indicates the 1014-block framing was malformed (used only
// for cases Unblock1014 genuinely cannot recover from, not for unexpected
// padding byte values).
var ErrBadBlocking = errors.New("vbs: invalid 1014-byte block framing")

// Unblock1014 strips IBM 1014-byte block framing from r, returning a reader
// that yields the raw VBS byte stream. Every 1014-byte chunk contributes its
// first 1012 bytes to the output; the trailing 2 bytes are discarded without
// checking their value. A final short chunk (file length not an exact
// multiple of 1014) is passed through as-is rather than rejected, since
// real-world files are not always padded out perfectly.
func Unblock1014(r io.Reader) (io.Reader, error) {
	return &unblock1014Reader{r: r, remaining: blockDataSize}, nil
}

// Read synchronously so a caller can stop after sniffing the first record.
type unblock1014Reader struct {
	r         io.Reader
	remaining int
}

func (r *unblock1014Reader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.remaining == 0 {
		var filler [2]byte
		if _, err := io.ReadFull(r.r, filler[:]); err != nil {
			if err == io.ErrUnexpectedEOF {
				err = io.EOF
			}
			return 0, err
		}
		r.remaining = blockDataSize
	}
	if len(p) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.r.Read(p)
	r.remaining -= n
	return n, err
}

// Block1014Writer wraps an io.Writer, applying IBM 1014-byte block framing to
// everything written through it. Callers must call Close to flush the final
// (padded) block.
type Block1014Writer struct {
	w         io.Writer
	remaining int
}

// NewBlock1014Writer creates a new 1014-byte blocking writer around w.
func NewBlock1014Writer(w io.Writer) *Block1014Writer {
	return &Block1014Writer{w: w, remaining: blockDataSize}
}

func (b *Block1014Writer) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		if len(p) < b.remaining {
			if _, err := b.w.Write(p); err != nil {
				return 0, err
			}
			b.remaining -= len(p)
			return total, nil
		}
		if _, err := b.w.Write(p[:b.remaining]); err != nil {
			return 0, err
		}
		if _, err := b.w.Write([]byte{writerPadChar, writerPadChar}); err != nil {
			return 0, err
		}
		p = p[b.remaining:]
		b.remaining = blockDataSize
	}
	return total, nil
}

// Close finalises the last (possibly partial) block with padding.
func (b *Block1014Writer) Close() error {
	pad := make([]byte, b.remaining+2)
	for i := range pad {
		pad[i] = writerPadChar
	}
	_, err := b.w.Write(pad)
	b.remaining = blockDataSize
	return err
}

// Reader reads successive VBS records from an underlying byte stream.
type Reader struct {
	r            *bufio.Reader
	RecordNumber int
}

// NewReader creates a VBS record reader. If blocked is true, the underlying
// stream is assumed to have IBM 1014-byte block framing and is unwrapped
// first.
func NewReader(r io.Reader, blocked bool) (*Reader, error) {
	if blocked {
		unblocked, err := Unblock1014(r)
		if err != nil {
			return nil, err
		}
		r = unblocked
	}
	return &Reader{r: bufio.NewReaderSize(r, 64*1024), RecordNumber: 1}, nil
}

// Next reads and returns the next record's payload (length prefix already
// stripped). Returns io.EOF when the VBS zero-length terminator record is
// reached, or when the stream simply runs out of data.
//
// Not all real-world VBS producers write the formal zero-length terminator
// record - some files just end after the last real record. If fewer than 4
// bytes remain when a length prefix is expected, that is treated the same
// as a clean EOF rather than an error (matching cardutil's VbsReader, whose
// docstring notes exactly this case: "the reader will just accept we are at
// end if this happens").
func (r *Reader) Next() ([]byte, error) {
	var lenBuf [4]byte
	n, err := io.ReadFull(r.r, lenBuf[:])
	if err != nil {
		if n < 4 {
			return nil, io.EOF
		}
		return nil, err
	}
	length := binary.BigEndian.Uint32(lenBuf[:])
	if length == 0 {
		return nil, io.EOF
	}
	if length > maxRecordLength {
		return nil, fmt.Errorf("vbs: record length %d exceeds sanity maximum", length)
	}
	rec := make([]byte, length)
	if _, err := io.ReadFull(r.r, rec); err != nil {
		return nil, fmt.Errorf("vbs: record %d: truncated (wanted %d bytes): %v", r.RecordNumber, length, err)
	}
	r.RecordNumber++
	return rec, nil
}

// Writer writes successive VBS records, terminating the stream with a
// zero-length record on Close.
type Writer struct {
	w io.Writer
}

// NewWriter creates a VBS record writer.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w}
}

// Write writes a single VBS record (its 4-byte length prefix is added
// automatically).
func (w *Writer) Write(rec []byte) error {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(rec)))
	if _, err := w.w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err := w.w.Write(rec)
	return err
}

// Close writes the VBS zero-length terminator record.
func (w *Writer) Close() error {
	var zero [4]byte
	_, err := w.w.Write(zero[:])
	return err
}
