// Package iso8583 implements the bitmap-driven message structure used by
// Mastercard IPM clearing messages: a 4-byte MTI, a 16-byte primary+
// secondary bitmap, followed by data elements in ascending bit order, each
// either fixed-length or prefixed with a 2- or 3-digit length field
// (LLVAR / LLLVAR). Private Data Subelements (PDS) are carried inside
// certain container data elements (DE 48, 62, 123, 124, 125) as repeated
// TLV entries (4-digit tag + 3-digit length + data).
package iso8583

import (
	"fmt"
	"strconv"
	"strings"

	"mciipm/internal/catalog"
	"mciipm/internal/ebcdic"
)

// Encoding identifies the text encoding used for numeric/text fields in a
// raw record. Binary fields (DE 55) are never text-decoded.
type Encoding string

const (
	EncodingASCII  Encoding = "ascii"
	EncodingEBCDIC Encoding = "ebcdic"
)

// Field is a single decoded tag/value pair, in the order it was encountered
// while parsing (PDS container fields are expanded in place into their
// constituent PDSxxxx entries rather than emitted as one raw DE value).
type Field struct {
	Tag   string
	Value string
}

// Message is a fully decoded IPM message (one VBS record).
type Message struct {
	MTI          string
	Fields       []Field
	Values       map[string]string // tag -> value, flattened for convenience
	SetBits      []int             // which DE bit numbers (2-128) were present
	Warnings     []string          // non-fatal issues encountered while parsing (e.g. duplicate PDS tags)
	SecondaryBit bool              // bit 1 of the primary bitmap; the manual requires this to always be 1
}

// Get returns the value for a tag ("MTI", "DE2", "PDS0501", ...) and whether
// it was present.
func (m *Message) Get(tag string) (string, bool) {
	v, ok := m.Values[tag]
	return v, ok
}

// HasBit reports whether the given DE bit number (2-128) was present in the
// bitmap. This is needed for PDS container fields (DE 48/62/123/124/125):
// they are expanded into their constituent PDSxxxx entries rather than kept
// as a raw "DEnn" value, so presence must be checked against the bitmap
// itself rather than the Values map.
func (m *Message) HasBit(deNum int) bool {
	for _, b := range m.SetBits {
		if b == deNum {
			return true
		}
	}
	return false
}

// FunctionCode is a convenience accessor for DE24, which together with the
// MTI identifies the specific clearing transaction function.
func (m *Message) FunctionCode() string {
	v, _ := m.Get("DE24")
	return v
}

// ParseMessage decodes a single raw VBS record payload (length prefix
// already stripped) into a Message.
func ParseMessage(raw []byte, enc Encoding) (*Message, error) {
	if len(raw) < 4+16 {
		return nil, fmt.Errorf("record too short: %d bytes (need at least 20 for MTI+bitmap)", len(raw))
	}

	p := &parser{data: raw, enc: enc}

	mtiBytes := p.takeRaw(4)
	mti := p.decodeText(mtiBytes)
	if strings.Trim(mti, "0123456789") != "" {
		return nil, fmt.Errorf("MTI %q is not numeric (wrong encoding? try the other --encoding value)", mti)
	}

	bitmapBytes := p.takeRaw(16)
	bm := ParseBitmap(bitmapBytes)

	msg := &Message{
		MTI:          mti,
		Values:       map[string]string{"MTI": mti},
		SetBits:      bm.SetBits(),
		SecondaryBit: bm.SecondaryPresent(),
	}
	msg.Fields = append(msg.Fields, Field{"MTI", mti})

	for _, deNum := range msg.SetBits {
		def, ok := catalog.Fields[strconv.Itoa(deNum)]
		if !ok {
			return nil, fmt.Errorf("DE%d is flagged present in the bitmap but has no catalog definition (unsupported/unused-in-IPM data element); cannot determine its length to continue parsing", deNum)
		}

		value, isBinary, err := p.readField(def)
		if err != nil {
			return nil, fmt.Errorf("DE%d (%s): %w", deNum, def.Name, err)
		}

		tag := "DE" + strconv.Itoa(deNum)

		if pdsContainerDEs[deNum] {
			pdsValues, perr := ParsePDS(value)
			if perr != nil {
				return nil, fmt.Errorf("DE%d (%s): %w", deNum, def.Name, perr)
			}
			for pdsTag, pdsVal := range pdsValues {
				if _, dup := msg.Values[pdsTag]; dup {
					msg.Warnings = append(msg.Warnings, fmt.Sprintf("%s appears in more than one PDS container (DE%d among others); later value wins", pdsTag, deNum))
				}
				msg.Values[pdsTag] = pdsVal
			}
			// Preserve encounter order deterministically by tag for the
			// fields slice (map iteration order is randomised in Go).
			for _, pdsTag := range sortedPDSKeys(pdsValues) {
				msg.Fields = append(msg.Fields, Field{pdsTag, pdsValues[pdsTag]})
			}
			continue
		}

		if isBinary {
			value = hexEncode(value)
		}
		msg.Values[tag] = value
		msg.Fields = append(msg.Fields, Field{tag, value})
	}

	if p.pos != len(p.data) {
		return nil, fmt.Errorf("message data not fully consumed: %d bytes parsed, %d bytes in record (trailing garbage or a field length mismatch)", p.pos, len(p.data))
	}

	return msg, nil
}

// parser walks raw bytes left to right, tracking position.
type parser struct {
	data []byte
	pos  int
	enc  Encoding
}

func (p *parser) takeRaw(n int) []byte {
	b := p.data[p.pos : p.pos+n]
	p.pos += n
	return b
}

func (p *parser) decodeText(b []byte) string {
	if p.enc == EncodingEBCDIC {
		return string(ebcdic.Decode(b))
	}
	return string(b)
}

// readField reads one data element's value per its catalog definition,
// returning the decoded value and whether it is a binary-representation
// field (in which case the caller should hex-encode it for safe CSV/text
// output rather than treat it as EBCDIC/ASCII text).
func (p *parser) readField(def catalog.FieldDef) (string, bool, error) {
	isBinary := def.Representation == "b"

	var length int
	switch def.FieldType {
	case catalog.Fixed:
		length = def.MaxLength
	case catalog.LLVAR:
		if p.pos+2 > len(p.data) {
			return "", isBinary, fmt.Errorf("truncated 2-digit length prefix")
		}
		lstr := p.decodeText(p.takeRaw(2))
		l, err := strconv.Atoi(lstr)
		if err != nil || strings.Trim(lstr, "0123456789") != "" {
			return "", isBinary, fmt.Errorf("invalid LLVAR length %q", lstr)
		}
		length = l
	case catalog.LLLVAR:
		if p.pos+3 > len(p.data) {
			return "", isBinary, fmt.Errorf("truncated 3-digit length prefix")
		}
		lstr := p.decodeText(p.takeRaw(3))
		l, err := strconv.Atoi(lstr)
		if err != nil || strings.Trim(lstr, "0123456789") != "" {
			return "", isBinary, fmt.Errorf("invalid LLLVAR length %q", lstr)
		}
		length = l
	default:
		return "", isBinary, fmt.Errorf("unknown field type %q", def.FieldType)
	}

	if length > def.MaxLength {
		return "", isBinary, fmt.Errorf("declared length %d exceeds field maximum %d", length, def.MaxLength)
	}
	if p.pos+length > len(p.data) {
		return "", isBinary, fmt.Errorf("declared length %d exceeds remaining record data (%d bytes left)", length, len(p.data)-p.pos)
	}
	raw := p.takeRaw(length)
	if isBinary {
		return string(raw), true, nil
	}
	return p.decodeText(raw), false, nil
}

func hexEncode(s string) string {
	const hexDigits = "0123456789ABCDEF"
	b := []byte(s)
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = hexDigits[c>>4]
		out[i*2+1] = hexDigits[c&0x0F]
	}
	return string(out)
}

func sortedPDSKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// simple insertion sort - PDS tag sets per message are small (<20 typically)
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}

// DetectEncoding sniffs the first record's MTI bytes to guess whether the
// file is ASCII/Latin-1 or EBCDIC (CP037) encoded.
func DetectEncoding(firstRecord []byte) Encoding {
	if len(firstRecord) < 4 {
		return EncodingASCII
	}
	if ebcdic.LooksLikeEBCDIC(firstRecord[:4]) {
		return EncodingEBCDIC
	}
	return EncodingASCII
}
