package iso8583

import (
	"fmt"
	"strconv"
	"strings"
)

// pdsContainerDEs lists the standard data elements that carry a sequence of
// PDS (Private Data Subelement) TLV entries: 4-digit numeric tag + 3-digit
// numeric length + that many bytes of data, repeated until the container's
// data is exhausted. Per Chapter 7 / Chapter 8 of the manual.
var pdsContainerDEs = map[int]bool{
	48:  true,
	62:  true,
	123: true,
	124: true,
	125: true,
}

// ParsePDS decodes a PDS container field's data into individual PDS tag/value
// pairs. Values are returned as raw decoded strings (already ASCII, not
// EBCDIC - callers must decode the whole record before calling this).
func ParsePDS(data string) (map[string]string, error) {
	out := make(map[string]string)
	pos := 0
	for pos < len(data) {
		if pos+7 > len(data) {
			return out, fmt.Errorf("pds: truncated tag/length header at offset %d", pos)
		}
		tag := data[pos : pos+4]
		if strings.Trim(tag, "0123456789") != "" {
			return out, fmt.Errorf("pds: invalid tag %q at offset %d", tag, pos)
		}
		lengthStr := data[pos+4 : pos+7]
		length, err := strconv.Atoi(lengthStr)
		if err != nil || strings.Trim(lengthStr, "0123456789") != "" {
			return out, fmt.Errorf("pds: invalid length field %q at offset %d", lengthStr, pos)
		}
		valStart := pos + 7
		valEnd := valStart + length
		if valEnd > len(data) {
			return out, fmt.Errorf("pds: PDS%s declares length %d but only %d bytes remain", tag, length, len(data)-valStart)
		}
		out["PDS"+tag] = data[valStart:valEnd]
		pos = valEnd
	}
	return out, nil
}
