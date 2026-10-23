package iso8583

// Bitmap represents the 128-bit (16-byte) primary+secondary bitmap that
// precedes the data elements in every IPM message. Per the manual: "All
// Mastercard IPM messages have mandatory data elements from both the
// Primary and Secondary Bit Maps. Therefore, both Primary and Secondary
// Bit Maps are required in all IPM messages" - so unlike generic ISO 8583,
// IPM messages always carry the full 16 bytes.
type Bitmap struct {
	raw [16]byte
}

// ParseBitmap reads a 16-byte bitmap.
func ParseBitmap(b []byte) Bitmap {
	var bm Bitmap
	copy(bm.raw[:], b)
	return bm
}

// IsSet reports whether the given bit number (1-128) is set. Bit 1 is the
// "secondary bitmap present" flag; bits 2-128 correspond to DE2-DE128.
func (bm Bitmap) IsSet(bitNum int) bool {
	if bitNum < 1 || bitNum > 128 {
		return false
	}
	idx := (bitNum - 1) / 8
	shift := 7 - ((bitNum - 1) % 8)
	return bm.raw[idx]&(1<<uint(shift)) != 0
}

// Set sets or clears the given bit number (1-128).
func (bm *Bitmap) Set(bitNum int, value bool) {
	if bitNum < 1 || bitNum > 128 {
		return
	}
	idx := (bitNum - 1) / 8
	shift := 7 - ((bitNum - 1) % 8)
	if value {
		bm.raw[idx] |= 1 << uint(shift)
	} else {
		bm.raw[idx] &^= 1 << uint(shift)
	}
}

// Bytes returns the raw 16-byte bitmap.
func (bm Bitmap) Bytes() []byte {
	out := make([]byte, 16)
	copy(out, bm.raw[:])
	return out
}

// SecondaryPresent reports whether bit 1 (secondary bitmap present) is set.
func (bm Bitmap) SecondaryPresent() bool {
	return bm.IsSet(1)
}

// SetBits returns the list of data element numbers (2-128) present in the
// bitmap, in ascending order.
func (bm Bitmap) SetBits() []int {
	var bits []int
	for i := 2; i <= 128; i++ {
		if bm.IsSet(i) {
			bits = append(bits, i)
		}
	}
	return bits
}
