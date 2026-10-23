package catalog

import (
	_ "embed"
	"encoding/json"
)

//go:embed control_records.json
var controlJSON []byte

// ControlLayout is one file/batch control record layout (TC 90 File
// Header, TC 91 Batch Trailer, TC 92 File Trailer), extracted from the
// "BASE II Clearing Interchange Formats, TC 50 to TC 92" manual
// (13 April 2019 edition) - the companion volume to the TC 01-49 manual
// the Draft Data catalog came from.
//
// Positions are expressed in CTF coordinates for every variant: an ITF
// record carries a 2-byte hash total at positions 3-4 that the reader
// strips before anything downstream sees the record, so an ITF layout's
// documented positions are shifted back by two from position 5 onward.
type ControlLayout struct {
	TransactionCodes []string   `json:"transaction_codes"`
	Format           string     `json:"format"`    // "ctf" or "itf"
	Direction        string     `json:"direction"` // "incoming", "outgoing", or "both"
	Title            string     `json:"title"`
	Fields           []FieldDef `json:"fields"`
}

// ControlLayouts is every documented control-record layout.
var ControlLayouts []ControlLayout

func init() {
	if err := json.Unmarshal(controlJSON, &ControlLayouts); err != nil {
		panic("catalog: failed to parse embedded control_records.json: " + err.Error())
	}
}

// LookupControlLayout returns the layout for a control record given its
// Transaction Code and the format the file was opened as.
//
// TC 90 is documented in four variants: outgoing and incoming, each in
// CTF and ITF. Nothing in the record itself says which direction it is,
// and the two share identical field boundaries - the incoming layout
// simply names two stretches (Settlement Date, Release Number) that the
// outgoing layout leaves as Reserved. The incoming layout is used, since
// this is a tool for inspecting files you have received; on an outgoing
// file those two fields are reserved space and will read as zeros or
// blanks rather than being misaligned.
func LookupControlLayout(transactionCode, format string) (ControlLayout, bool) {
	if format != "itf" {
		format = "ctf"
	}
	for _, l := range ControlLayouts {
		if l.Format != format {
			continue
		}
		if l.Direction == "outgoing" {
			continue
		}
		for _, c := range l.TransactionCodes {
			if c == transactionCode {
				return l, true
			}
		}
	}
	return ControlLayout{}, false
}

// IsControlRecord reports whether a Transaction Code is one of the
// file/batch control records.
func IsControlRecord(code string) bool {
	_, ok := ControlRecords[code]
	return ok
}
