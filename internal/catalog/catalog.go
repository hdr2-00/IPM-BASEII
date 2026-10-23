// Package catalog provides the Mastercard IPM data element and message
// layout catalog, extracted programmatically from the "IPM Clearing
// Formats" manual (Chapters 2, 3 and 7). It is used by the parser to know
// each field's wire format (fixed/LLVAR/LLLVAR + length) and by the
// validator to know which fields are mandatory/conditional/optional for a
// given message type.
package catalog

import (
	_ "embed"
	"encoding/json"
)

//go:embed fields.json
var fieldsJSON []byte

//go:embed messages.json
var messagesJSON []byte

// FieldType describes how a data element's length is encoded on the wire.
type FieldType string

const (
	Fixed  FieldType = "FIXED"
	LLVAR  FieldType = "LLVAR"
	LLLVAR FieldType = "LLLVAR"
)

// FieldDef describes one ISO 8583 Data Element (DE 1-127) as defined in
// Chapter 7 of the IPM Clearing Formats manual.
type FieldDef struct {
	Name           string    `json:"name"`
	FieldType      FieldType `json:"field_type"`
	MaxLength      int       `json:"max_length"`
	Representation string    `json:"representation"` // n, a, an, ans, b, etc.
}

// FieldReq is one row of a per-message-type field requirement table
// (Chapters 2 and 3): a DE or PDS tag together with its Org(inator) /
// Sys(tem) / Dst(ination) requirement code.
//
// Requirement codes: M = Mandatory, C = Conditional, O = Optional,
// X = system-generated (inserted by Mastercard, not sent by originator),
// • = not applicable / not present in this message.
type FieldReq struct {
	Tag  string `json:"tag"`
	Name string `json:"name"`
	Org  string `json:"org"`
	Sys  string `json:"sys"`
	Dst  string `json:"dst"`
}

// MessageDef describes the full field layout for one specific message type,
// keyed by "MTI:FunctionCode" (and, for Financial Detail Addendum messages,
// further qualified by PDS 0501 Usage Code and Industry Record Number).
type MessageDef struct {
	Title  string     `json:"title"`
	Fields []FieldReq `json:"fields"`
}

var (
	// Fields maps DE number (as string, e.g. "2", "48") to its definition.
	Fields map[string]FieldDef

	// Messages maps a canonical message key to its field layout. Keys:
	//   "1240:200"              First Presentment
	//   "1240:205", "1240:282"  Second Presentment (full / partial)
	//   "1442:450/451/453/454"  First / Arbitration Chargeback (full / partial)
	//   "1644:696:<UC>:<IRN>"   Financial Detail Addendum, by Usage Code + Industry Record Number
	//   "1644:697:ORG"/"DST"    File Header, customer- / Mastercard-generated
	//   "1644:695:ORG"/"DST"    File Trailer, customer- / Mastercard-generated
	Messages map[string]MessageDef
)

func init() {
	Fields = make(map[string]FieldDef)
	if err := json.Unmarshal(fieldsJSON, &Fields); err != nil {
		panic("catalog: failed to parse embedded fields.json: " + err.Error())
	}
	Messages = make(map[string]MessageDef)
	if err := json.Unmarshal(messagesJSON, &Messages); err != nil {
		panic("catalog: failed to parse embedded messages.json: " + err.Error())
	}
}

// UsageCodeAddendum maps a Financial Detail Addendum's PDS 0501 (Transaction
// Description) subfield 1 (Usage Code) + subfield 2 (Industry Record Number)
// to the specific addendum type name, per the manual's "PDS 0501 Subfields"
// section. Only Usage Code + Industry Record Number together uniquely
// identify the addendum layout - Usage Code alone is ambiguous for several
// types (e.g. 01, 09, 11, 14, 98 each cover more than one layout).
var UsageCodeAddendum = map[[2]string]string{
	{"01", "000"}: "Passenger Transport Detail—General Ticket Information",
	{"01", "001"}: "Passenger Transport Detail—Trip Leg Data",
	{"01", "002"}: "Passenger Transport Detail—Rail Data",
	{"05", "000"}: "Vehicle Rental Detail",
	{"06", "000"}: "Lodging Summary",
	{"07", "000"}: "Temporary Services",
	{"08", "000"}: "Shipping/Courier Services",
	{"09", "000"}: "Electronic Invoice—Transaction Data",
	{"09", "001"}: "Electronic Invoice—Party Information",
	{"10", "000"}: "Payment Transaction Detail",
	{"11", "000"}: "Telephony Billing—Summary",
	{"11", "001"}: "Telephony Billing—Detail",
	{"12", "000"}: "Travel Agency Detail",
	{"13", "000"}: "Lodged Account Detail",
	{"14", "000"}: "Private Label Common Data",
	{"14", "001"}: "Private Label Line Item Detail",
	{"15", "000"}: "Healthcare-IIAS Detail",
	{"98", "000"}: "Corporate Detail—Corporate Card Common Data Requirements",
	{"98", "002"}: "Corporate Detail—Corporate Fleet Transaction Information",
	{"98", "950"}: "Corporate Detail—Corporate Line Item Detail",
	{"99", "000"}: "Generic Detail",
}

// TransactionFunction describes what a given MTI + Function Code combination
// means, per Chapter 7 DE 24 (Function Code) "Values" table.
type TransactionFunction struct {
	MTI          string
	FunctionCode string
	Name         string
	// Category groups related function codes for summary reporting.
	Category string
}

// FunctionCodes is the full set of MTI/Function Code combinations the
// clearing system supports, transcribed from the manual.
var FunctionCodes = []TransactionFunction{
	{"1240", "200", "First Presentment", "Presentment"},
	{"1240", "205", "Second Presentment (Full)", "Presentment"},
	{"1240", "282", "Second Presentment (Partial)", "Presentment"},
	{"1442", "450", "First Chargeback (Full)", "Chargeback"},
	{"1442", "451", "Arbitration Chargeback (Full)", "Chargeback"},
	{"1442", "453", "First Chargeback (Partial)", "Chargeback"},
	{"1442", "454", "Arbitration Chargeback (Partial)", "Chargeback"},
	{"1644", "696", "Financial Detail Addendum", "Addendum"},
	{"1644", "697", "File Header", "File Control"},
	{"1644", "695", "File Trailer", "File Control"},
	{"1644", "699", "File Reject", "File Control"},
	{"1644", "691", "Message Exception", "Administrative"},
	{"1644", "693", "Text Message", "Administrative"},
	{"1644", "603", "Retrieval Request", "Retrieval/Fee"},
	{"1644", "605", "Retrieval Request Acknowledgement", "Retrieval/Fee"},
	{"1644", "640", "Currency Update", "Administrative"},
	{"1644", "680", "File Currency Summary", "Reconciliation"},
	{"1644", "685", "Financial Position Detail", "Reconciliation"},
	{"1644", "688", "Settlement Position Detail", "Reconciliation"},
	{"1740", "700", "Fee Collection (Customer-generated)", "Fee Collection"},
	{"1740", "780", "Fee Collection Return", "Fee Collection"},
	{"1740", "781", "Fee Collection Resubmission", "Fee Collection"},
	{"1740", "782", "Fee Collection Arbitration Return", "Fee Collection"},
	{"1740", "783", "Fee Collection (Clearing System-generated)", "Fee Collection"},
	{"1740", "790", "Fee Collection (Funds Transfer)", "Fee Collection"},
	{"1740", "791", "Fee Collection (Funds Transfer Backout)", "Fee Collection"},
}

// funcLookup indexes FunctionCodes by "MTI:FunctionCode".
var funcLookup map[string]TransactionFunction

func init() {
	funcLookup = make(map[string]TransactionFunction, len(FunctionCodes))
	for _, f := range FunctionCodes {
		funcLookup[f.MTI+":"+f.FunctionCode] = f
	}
}

// LookupFunction returns the transaction function for a given MTI+DE24, and
// whether the combination is recognised at all.
func LookupFunction(mti, functionCode string) (TransactionFunction, bool) {
	f, ok := funcLookup[mti+":"+functionCode]
	return f, ok
}

// PresentmentChargebackKeys is the set of MTI:FunctionCode combinations that
// are in scope for the converter/validator (presentments, addenda and
// chargebacks) as opposed to file control, reconciliation, fee collection,
// retrieval, or administrative messages.
var PresentmentChargebackKeys = map[string]bool{
	"1240:200": true,
	"1240:205": true,
	"1240:282": true,
	"1442:450": true,
	"1442:451": true,
	"1442:453": true,
	"1442:454": true,
	"1644:696": true,
}

// IsPresentmentOrChargeback reports whether the given MTI+Function Code is
// a First/Second Presentment, First/Arbitration Chargeback, or Financial
// Detail Addendum message.
func IsPresentmentOrChargeback(mti, functionCode string) bool {
	return PresentmentChargebackKeys[mti+":"+functionCode]
}
