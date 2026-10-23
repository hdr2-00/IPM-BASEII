// Package catalog provides the Visa BASE II TC 05 (Draft Data) field
// catalog, extracted programmatically from the "BASE II Clearing
// Interchange Formats, TC 01 to TC 49" manual (19 October 2019 edition).
//
// Draft-data layouts use 168-byte CTF coordinates. The reader removes
// the extra two ITF hash bytes before field extraction.
package catalog

import (
	_ "embed"
	"encoding/json"
)

//go:embed tc05_fields.json
var tc05JSON []byte

// FieldDef is one fixed-position field within a TCR layout.
type FieldDef struct {
	Start  int    `json:"start"`
	End    int    `json:"end"`
	Length int    `json:"length"`
	Format string `json:"format"` // UN=unpacked numeric, AN=alphanumeric, ANS, DX=display hex, N
	Name   string `json:"name"`
}

// Discriminator identifies which of several same-TCR-number layouts a
// record uses, via the "Business Format Code" (or equivalent) field value
// documented for that specific variant.
type Discriminator struct {
	FieldStart int    `json:"field_start"`
	FieldEnd   int    `json:"field_end"`
	Value      string `json:"value"`
}

// TCRLayout is one complete field layout: either a TCR number's only
// layout (TCR0, TCR1, TCR5, TCR6, TCR7, TCRE), or one of several variants
// sharing that TCR number, disambiguated by Discriminator (TCR3, TCR4,
// TCRD) or not automatically classifiable at all from the record's own
// bytes (TCR2 - country-specific, depends on external context).
type TCRLayout struct {
	TCR              string         `json:"tcr"` // "TCR0".."TCR7", "TCRD", "TCRE"
	Variant          string         `json:"variant"`
	FieldCount       int            `json:"field_count"`
	Fields           []FieldDef     `json:"fields"`
	Discriminator    *Discriminator `json:"discriminator,omitempty"`
	CountryHint      string         `json:"country_hint,omitempty"`
	AutoClassifiable bool           `json:"auto_classifiable"`
}

// TC05Layouts is every TCR layout documented for the TC 05 family
// (Draft Data: TC 05/06/07/15/16/17/25/26/27/35/36/37 all share the same
// TCR layouts per the manual's own chapter scope note).
var TC05Layouts []TCRLayout

// byTCR indexes TC05Layouts by TCR number/letter ("TCR0", "TCR3", ...).
var byTCR map[string][]TCRLayout

func init() {
	if err := json.Unmarshal(tc05JSON, &TC05Layouts); err != nil {
		panic("catalog: failed to parse embedded tc05_fields.json: " + err.Error())
	}
	byTCR = make(map[string][]TCRLayout)
	for _, l := range TC05Layouts {
		byTCR[l.TCR] = append(byTCR[l.TCR], l)
	}
}

// LayoutsForTCR returns every documented layout for a given TCR number
// ("TCR0".."TCR7", "TCRD", "TCRE"). Most return exactly one; TCR2, TCR3,
// TCR4, and TCRD return several (see ResolveLayout for picking the right
// one from actual record bytes).
func LayoutsForTCR(tcr string) []TCRLayout {
	return byTCR[tcr]
}

// ResolveLayout picks the correct layout for a raw 168-byte TCR record,
// given its TCR number/letter. For TCRs with only one documented layout,
// that's returned directly. For TCRs with several variants distinguished
// by a discriminator field (TCR3, TCR4, TCRD), the variant whose
// discriminator value matches the record's bytes is returned. For TCR2
// (country-specific with no in-record discriminator), ok is false - the
// caller should fall back to raw positional capture.
func ResolveLayout(tcr string, recordText string) (layout TCRLayout, ok bool) {
	candidates := byTCR[tcr]
	if len(candidates) == 0 {
		return TCRLayout{}, false
	}
	if len(candidates) == 1 && candidates[0].AutoClassifiable {
		return candidates[0], true
	}
	for _, c := range candidates {
		if c.Discriminator == nil {
			continue
		}
		d := c.Discriminator
		if d.FieldEnd > len(recordText) || d.FieldStart < 1 {
			continue
		}
		val := recordText[d.FieldStart-1 : d.FieldEnd]
		if val == d.Value {
			return c, true
		}
	}
	// No discriminator matched (or none classifiable, e.g. TCR2): fall back
	// to the first entry that isn't itself a real variant match, so callers
	// at least get a name/hint rather than nothing.
	return candidates[0], false
}

// TransactionFunction describes what a given Transaction Code means, per
// Chapter 4's "Transaction Code" edit-criteria value list (all Draft Data
// TCR0/TCR1/etc. layouts are shared across this whole family of codes).
type TransactionFunction struct {
	Code     string
	Name     string
	Category string
}

// DraftDataFunctions is the Transaction Code value list documented for
// TCR0 (which doubles as the family of transaction codes sharing every
// TC05 TCR layout).
var DraftDataFunctions = []TransactionFunction{
	{"05", "Sales Draft", "Presentment"},
	{"06", "Credit Voucher", "Presentment"},
	{"07", "Cash Disbursement", "Presentment"},
	{"15", "Sales Draft Dispute Financial", "Chargeback"},
	{"16", "Credit Voucher Dispute Financial", "Chargeback"},
	{"17", "Cash Disbursement Dispute Financial", "Chargeback"},
	{"25", "Sales Draft Reversal", "Reversal"},
	{"26", "Credit Voucher Reversal", "Reversal"},
	{"27", "Cash Disbursement Reversal", "Reversal"},
	{"35", "Sales Draft Dispute Financial Reversal", "Reversal"},
	{"36", "Credit Voucher Dispute Financial Reversal", "Reversal"},
	{"37", "Cash Disbursement Dispute Financial Reversal", "Reversal"},
}

// ControlRecords names file/batch control codes. Their field layouts
// are stored separately in control_records.json.
var ControlRecords = map[string]TransactionFunction{
	"90": {"90", "File Header", "File Control"},
	"91": {"91", "Batch Trailer", "File Control"},
	"92": {"92", "File Trailer", "File Control"},
}

var funcByCode map[string]TransactionFunction

func init() {
	funcByCode = make(map[string]TransactionFunction, len(DraftDataFunctions)+len(ControlRecords))
	for _, f := range DraftDataFunctions {
		funcByCode[f.Code] = f
	}
	for code, f := range ControlRecords {
		funcByCode[code] = f
	}
}

// LookupFunction returns what a Transaction Code means, and whether it's
// recognised at all (either as a Draft Data family code or a control
// record).
func LookupFunction(code string) (TransactionFunction, bool) {
	f, ok := funcByCode[code]
	return f, ok
}

// IsDraftDataFamily reports whether code is one of the TC05-shared-layout
// Draft Data transaction codes (as opposed to a control record or a
// Transaction Code from a different, uncatalogued chapter).
func IsDraftDataFamily(code string) bool {
	_, ok := funcByCode[code]
	if !ok {
		return false
	}
	for _, f := range DraftDataFunctions {
		if f.Code == code {
			return true
		}
	}
	return false
}
