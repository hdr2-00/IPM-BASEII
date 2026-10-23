package baseii

import (
	"regexp"
	"strings"

	"mciipm/internal/baseii/catalog"
	"mciipm/internal/ebcdic"
)

// Encoding identifies the text encoding of a raw record.
type Encoding string

const (
	EncodingASCII  Encoding = "ascii"
	EncodingEBCDIC Encoding = "ebcdic"
)

// Field is one decoded, named value within a TCR.
type Field struct {
	Tag    string // e.g. "TCR0.AccountNumber"
	Name   string
	Value  string
	Format string // UN, AN, ANS, DX, N - from the catalog's FieldDef
}

// TCRRecord is one decoded 168-byte Transaction Component Record.
type TCRRecord struct {
	SequenceNumber string // raw position-4 character: "0".."7", "D", "E", or something unrecognised
	TCRType        string // "TCR0".."TCR7", "TCRD", "TCRE", "TC90"/"TC91"/"TC92" for control records, or "" if unrecognised
	ControlTitle   string // set for control records: the layout's documented title
	Layout         catalog.TCRLayout
	Classified     bool // true if Layout was matched with confidence (single layout, or discriminator matched)
	Fields         []Field
	RawText        string // full 168-char decoded text
}

// Transaction is one logical BASE II transaction: a Transaction Code plus
// the ordered TCRs that make it up (TCR0 first, then whichever of
// TCR1/2/3/4/5/6/7/D/E that specific transaction carries).
type Transaction struct {
	TransactionNumber int // 1-based, in file order
	TransactionCode   string
	TCRs              []TCRRecord
	ParseErr          error // set if any TCR within this transaction failed to decode
}

// Qualifier returns the Transaction Code Qualifier from TCR0, if present
// (position 3 - default/account-funding/original-credit).
func (t *Transaction) Qualifier() string {
	if len(t.TCRs) == 0 || len(t.TCRs[0].RawText) < 3 {
		return ""
	}
	return t.TCRs[0].RawText[2:3]
}

// Get returns the value of a field by tag (e.g. "TCR0.AccountNumber")
// from anywhere within the transaction's TCRs.
func (t *Transaction) Get(tag string) (string, bool) {
	for _, tcr := range t.TCRs {
		for _, f := range tcr.Fields {
			if f.Tag == tag {
				return f.Value, true
			}
		}
	}
	return "", false
}

// tcrTypeForChar maps the raw Transaction Component Sequence Number
// character (position 4) to a catalog TCR key, and to a comparable
// ordinal for the "ascending within a transaction" boundary rule.
func tcrTypeForChar(c byte) (tcrType string, ordinal int, ok bool) {
	switch {
	case c >= '0' && c <= '7':
		return "TCR" + string(c), int(c - '0'), true
	case c == 'D':
		return "TCRD", 8, true
	case c == 'E':
		return "TCRE", 9, true
	default:
		return "", -1, false
	}
}

var pascalCaseStrip = regexp.MustCompile(`[^A-Za-z0-9]+`)

// fieldTag builds a compact, CSV-header-friendly tag from a TCR type and
// a field's descriptive name (BASE II has no compact numeric field codes
// the way Mastercard DE/PDS numbers do - the name itself is the field's
// canonical reference in this ecosystem, so it's used directly rather
// than a position number, which would be far less recognisable).
func fieldTag(tcrType, name string) string {
	parts := pascalCaseStrip.Split(name, -1)
	var b strings.Builder
	b.WriteString(tcrType)
	b.WriteByte('.')
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		if len(p) > 1 {
			b.WriteString(p[1:])
		}
	}
	return b.String()
}

// decodeTCR decodes one raw 168-byte record into a TCRRecord, resolving
// its specific layout (including Business-Format-Code-based
// disambiguation for TCR3/TCR4/TCRD, and best-effort raw capture for
// TCR2's country variants, which the manual documents but does not give
// an in-record discriminator for).
//
// Field-level decomposition is only attempted when the record's own
// Transaction Code (positions 1-2) is actually a member of the Draft Data
// family the catalog covers (TC 05/06/07/15/16/17/25/26/27/35/36/37) -
// this matters because position 4 (the TCR type indicator) is not on its
// own sufficient: a control record (e.g. TC 90 File Header) could
// legitimately contain a '0' at position 4 by coincidence, and applying
// the Draft Data TCR0 field catalog to it would silently fabricate
// plausible-looking but meaningless field values.
func decodeTCR(raw []byte, enc Encoding, format Format) TCRRecord {
	text := decodeText(raw, enc)
	rec := TCRRecord{RawText: text}

	if len(text) < 4 {
		return rec
	}
	tc := ""
	if len(text) >= 2 {
		tc = text[0:2]
	}
	if !catalog.IsDraftDataFamily(tc) {
		// File and batch control records (TC 90/91/92) have documented
		// layouts too - they just live in the companion TC 50-92 manual
		// volume rather than the Draft Data one.
		if layout, ok := catalog.LookupControlLayout(tc, string(format)); ok {
			rec.TCRType = "TC" + tc
			rec.ControlTitle = layout.Title
			rec.Classified = true
			rec.Fields = sliceFields(rec.TCRType, layout.Fields, text)
		}
		return rec
	}

	tcrType, _, ok := tcrTypeForChar(text[3])
	if !ok {
		return rec
	}
	rec.SequenceNumber = string(text[3])
	rec.TCRType = tcrType

	layout, matched := catalog.ResolveLayout(tcrType, text)
	rec.Layout = layout
	rec.Classified = matched

	if layout.TCR == "" {
		return rec
	}

	rec.Fields = sliceFields(tcrType, layout.Fields, text)
	return rec
}

// sliceFields cuts a decoded record's text into named values at a
// layout's documented positions. A field whose span runs past the end of
// the record is skipped rather than guessed at - real control records are
// routinely shorter than a full-length TCR.
func sliceFields(tag string, defs []catalog.FieldDef, text string) []Field {
	fields := make([]Field, 0, len(defs))
	for _, fd := range defs {
		if fd.End > len(text) || fd.Start < 1 {
			continue
		}
		fields = append(fields, Field{
			Tag:    fieldTag(tag, fd.Name),
			Name:   fd.Name,
			Value:  strings.TrimRight(text[fd.Start-1:fd.End], " "),
			Format: fd.Format,
		})
	}
	return fields
}

func decodeText(raw []byte, enc Encoding) string {
	if enc == EncodingEBCDIC {
		return string(ebcdic.Decode(raw))
	}
	return string(raw)
}

// DetectEncoding sniffs a raw record's Transaction Code bytes (positions
// 1-2) to guess ASCII vs EBCDIC, the same heuristic used for IPM: EBCDIC
// digits are 0xF0-0xF9, never valid as ASCII digits.
func DetectEncoding(rawFirstRecord []byte) Encoding {
	if len(rawFirstRecord) < 2 {
		return EncodingASCII
	}
	if ebcdic.LooksLikeEBCDIC(rawFirstRecord[:2]) {
		return EncodingEBCDIC
	}
	return EncodingASCII
}

func byteAt(s string, i int) byte {
	if i < 0 || i >= len(s) {
		return 0
	}
	return s[i]
}
