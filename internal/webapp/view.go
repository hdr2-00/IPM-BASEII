package webapp

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"mciipm/internal/baseii"
	"mciipm/internal/convert"
	"mciipm/internal/money"
	"mciipm/internal/validate"
)

// Scheme identifies which card network format a session's file was parsed
// as. The dashboard UI, once past the upload page, is otherwise identical
// for either - all templates operate on the generic view types below.
type Scheme string

const (
	SchemeMastercard Scheme = "mastercard"
	SchemeVisa       Scheme = "visa"
)

func (s Scheme) Label() string {
	if s == SchemeVisa {
		return "Visa BASE II"
	}
	return "Mastercard IPM"
}

// GenericSummary supplies the overview template for either network.
type GenericSummary struct {
	TotalRecords      int
	ParseErrors       int
	RecordsByFunction map[string]int
	RecordsByCategory map[string]int
	ErrorsByRule      map[string]int
	WarningsByRule    map[string]int
	TotalErrors       int
	TotalWarnings     int
	AmountTotals      map[string]*GenericCurrencyTotal
	Findings          []GenericFinding
}

type GenericCurrencyTotal struct {
	CurrencyCode string
	Function     string
	Direction    string
	MinorUnits   int64
	Count        int
	Exponent     int
	IsCredit     bool
}

// Display renders the total at its currency's own number of decimal
// places, rather than assuming two.
func (c GenericCurrencyTotal) Display() string { return money.Format(c.MinorUnits, c.Exponent) }

type GenericFinding struct {
	RecordNumber int
	Severity     string
	Rule         string
	Tag          string
	Message      string
}

func summaryFromMastercard(s validate.Summary) GenericSummary {
	amounts := make(map[string]*GenericCurrencyTotal, len(s.AmountTotals))
	for k, v := range s.AmountTotals {
		amounts[k] = &GenericCurrencyTotal{CurrencyCode: v.CurrencyCode, Function: v.Function, Direction: v.Direction,
			MinorUnits: v.MinorUnits, Count: v.Count, Exponent: v.Exponent, IsCredit: v.IsCredit()}
	}
	findings := make([]GenericFinding, len(s.Findings))
	for i, f := range s.Findings {
		findings[i] = GenericFinding{RecordNumber: f.RecordNumber, Severity: string(f.Severity), Rule: f.Rule, Tag: f.Tag, Message: maskFindingMessage(f.Tag, f.Message)}
	}
	return GenericSummary{
		TotalRecords: s.TotalRecords, ParseErrors: s.ParseErrors,
		RecordsByFunction: s.RecordsByFunction, RecordsByCategory: s.RecordsByCategory,
		ErrorsByRule: s.ErrorsByRule, WarningsByRule: s.WarningsByRule,
		TotalErrors: s.TotalErrors, TotalWarnings: s.TotalWarnings,
		AmountTotals: amounts, Findings: findings,
	}
}

func summaryFromVisa(s baseii.Summary) GenericSummary {
	amounts := make(map[string]*GenericCurrencyTotal, len(s.AmountTotals))
	for k, v := range s.AmountTotals {
		amounts[k] = &GenericCurrencyTotal{CurrencyCode: v.CurrencyCode, Function: v.Function, Direction: v.Direction,
			MinorUnits: v.MinorUnits, Count: v.Count, Exponent: v.Exponent, IsCredit: v.IsCredit()}
	}
	findings := make([]GenericFinding, len(s.Findings))
	for i, f := range s.Findings {
		findings[i] = GenericFinding{RecordNumber: f.TransactionNumber, Severity: string(f.Severity), Rule: f.Rule, Tag: f.Tag, Message: maskFindingMessage(f.Tag, f.Message)}
	}
	return GenericSummary{
		TotalRecords: s.TotalTransactions, ParseErrors: s.ParseErrors,
		RecordsByFunction: s.ByFunction, RecordsByCategory: s.ByCategory,
		ErrorsByRule: s.ErrorsByRule, WarningsByRule: s.WarningsByRule,
		TotalErrors: s.TotalErrors, TotalWarnings: s.TotalWarnings,
		AmountTotals: amounts, Findings: findings,
	}
}

// RecordRow supplies one table row, with separate counts for severity filters.
type RecordRow struct {
	RecordNumber int
	Identifier   string // MTI (Mastercard) or Transaction Code (Visa)
	Function     string
	AmountMinor  int64  // transaction amount in minor units
	Currency     string // currency code the amount is in
	Exponent     int    // implied decimal places for that currency
	IsCredit     bool   // money moving back to the cardholder, shown differently
	HasAmount    bool   // false for records that carry no amount at all
	Errors       int
	Warnings     int
}

// Status classifies a row for the file strip and the status column.
func (r RecordRow) Status() string {
	switch {
	case r.Errors > 0:
		return "error"
	case r.Warnings > 0:
		return "warning"
	}
	return "ok"
}

// Display renders the row's amount at its currency's decimal places.
func (r RecordRow) Display() string { return money.Format(r.AmountMinor, r.Exponent) }

// recordRowExponent prefers the message's own PDS 0148 (Currency
// Exponents) over the ISO 4217 table, matching the validator.
func recordRowExponent(rr convert.RecordResult, currency string) int {
	if pds, ok := rr.Msg.Get("PDS0148"); ok {
		if exp, found := money.ParseExponents(pds)[currency]; found {
			return exp
		}
	}
	return money.Exponent(currency)
}

// OK reports whether the record had no findings at all.
func (r RecordRow) OK() bool { return r.Errors == 0 && r.Warnings == 0 }

// panTags identifies PAN-bearing fields to mask in browser views.
// CSV exports retain the original values.
var panTags = map[string]bool{
	"DE2":                      true, // Primary Account Number
	"TCR0.AccountNumber":       true,
	"TCR3.SenderAccountNumber": true, // money-transfer sender PAN
}

// maskValue keeps the first six and last four characters of long values.
// Short values are masked entirely.
func maskValue(tag, value string) string {
	if !panTags[tag] {
		return value
	}
	v := strings.TrimSpace(value)
	if v == "" {
		return value
	}
	if len(v) <= 10 {
		return strings.Repeat("•", len(v))
	}
	return v[:6] + strings.Repeat("•", len(v)-10) + v[len(v)-4:]
}

// longDigitRun matches a card-number-length digit sequence.
var longDigitRun = regexp.MustCompile(`[0-9]{10,}`)

// maskFindingMessage masks PAN-length digit runs inside a finding's own
// message text. Some rules quote the offending value back to the operator
// (e.g. "DE2 must be all-numeric but got ..."), which would otherwise
// route a PAN around maskValue.
func maskFindingMessage(tag, msg string) string {
	if !panTags[tag] {
		return msg
	}
	return longDigitRun.ReplaceAllStringFunc(msg, func(s string) string {
		return s[:6] + strings.Repeat("•", len(s)-10) + s[len(s)-4:]
	})
}

// StripSegment is one block of the overview's file strip: a run of
// records collapsed into a single bar, carrying the worst status in the
// run. Long files are bucketed so the strip stays readable - a 50,000
// record file would otherwise be 50,000 sub-pixel slivers.
type StripSegment struct {
	FirstRecord int
	LastRecord  int
	Count       int
	Status      string
}

// Label is what the segment's tooltip says.
func (s StripSegment) Label() string {
	rec := "record"
	if s.Count != 1 {
		rec = "records"
	}
	if s.FirstRecord == s.LastRecord {
		return fmt.Sprintf("Record %d: %s", s.FirstRecord, s.Status)
	}
	return fmt.Sprintf("%d %s, %d to %d: worst status %s", s.Count, rec, s.FirstRecord, s.LastRecord, s.Status)
}

// buildFileStrip collapses the record list into at most maxSegments bars,
// each taking the worst status of the records it covers. Worst-wins
// matters: a single bad record inside a bucket of a hundred must still
// show, since finding exactly that is the point of the view.
func buildFileStrip(rows []RecordRow, maxSegments int) []StripSegment {
	if len(rows) == 0 || maxSegments < 1 {
		return nil
	}
	perSegment := 1
	if len(rows) > maxSegments {
		perSegment = (len(rows) + maxSegments - 1) / maxSegments
	}

	rank := map[string]int{"ok": 0, "warning": 1, "error": 2}
	var out []StripSegment
	for i := 0; i < len(rows); i += perSegment {
		end := i + perSegment
		if end > len(rows) {
			end = len(rows)
		}
		seg := StripSegment{
			FirstRecord: rows[i].RecordNumber,
			LastRecord:  rows[end-1].RecordNumber,
			Count:       end - i,
			Status:      "ok",
		}
		for _, row := range rows[i:end] {
			if rank[row.Status()] > rank[seg.Status] {
				seg.Status = row.Status()
			}
		}
		out = append(out, seg)
	}
	return out
}

// RecordDetail is the full detail view for one record/transaction.
type RecordDetail struct {
	RecordNumber int
	Identifier   string
	Function     string
	Recognised   bool
	ParseErr     string
	Findings     []GenericFinding
	Fields       []FieldRow
	NotFound     bool
}

// FieldRow is one tag/value pair shown in a record detail panel.
type FieldRow struct {
	Tag   string
	Value string
}

func recordRowFromMastercard(rr convert.RecordResult, findingsByRecord map[int][]validate.Finding) RecordRow {
	row := RecordRow{RecordNumber: rr.RecordNumber}
	for _, f := range findingsByRecord[rr.RecordNumber] {
		if f.Severity == validate.SeverityError {
			row.Errors++
		} else {
			row.Warnings++
		}
	}
	if rr.ParseErr != nil {
		row.Identifier = "—"
		row.Function = "(failed to parse)"
		return row
	}
	row.Identifier = rr.Msg.MTI
	if rr.Classify.Recognised {
		row.Function = rr.Classify.FunctionName
	} else {
		row.Function = "Unrecognised"
	}
	if amt, ok := rr.Msg.Get("DE4"); ok {
		if v, err := strconv.ParseInt(strings.TrimSpace(amt), 10, 64); err == nil {
			row.AmountMinor, row.HasAmount = v, true
			if cur, ok := rr.Msg.Get("DE49"); ok {
				row.Currency = cur
			}
			row.Exponent = recordRowExponent(rr, row.Currency)
			if pc, ok := rr.Msg.Get("DE3"); ok {
				row.IsCredit = validate.CardholderDirection(pc) == "credit"
			}
		}
	}
	return row
}

func recordDetailFromMastercard(rr convert.RecordResult, findings []validate.Finding) RecordDetail {
	d := RecordDetail{RecordNumber: rr.RecordNumber}
	if rr.ParseErr != nil {
		d.ParseErr = rr.ParseErr.Error()
		return d
	}
	d.Identifier = rr.Msg.MTI
	d.Recognised = rr.Classify.Recognised
	if rr.Classify.Recognised {
		d.Function = rr.Classify.FunctionName
	}
	for _, f := range rr.Msg.Fields {
		d.Fields = append(d.Fields, FieldRow{Tag: f.Tag, Value: maskValue(f.Tag, f.Value)})
	}
	for _, f := range findings {
		d.Findings = append(d.Findings, GenericFinding{RecordNumber: f.RecordNumber, Severity: string(f.Severity), Rule: f.Rule, Tag: f.Tag, Message: maskFindingMessage(f.Tag, f.Message)})
	}
	return d
}

func recordRowFromVisa(t baseii.Transaction, findingsByRecord map[int][]baseii.Finding) RecordRow {
	row := RecordRow{RecordNumber: t.TransactionNumber}
	for _, f := range findingsByRecord[t.TransactionNumber] {
		if f.Severity == baseii.SeverityError {
			row.Errors++
		} else {
			row.Warnings++
		}
	}
	if t.ParseErr != nil {
		row.Identifier = "—"
		row.Function = "(failed to parse)"
		return row
	}
	row.Identifier = t.TransactionCode
	if fn, ok := baseii.LookupFunctionName(t.TransactionCode); ok {
		row.Function = fn
	} else {
		row.Function = "Unrecognised"
	}
	if amt, cur, ok := t.Amount(); ok {
		if v, err := strconv.ParseInt(strings.TrimSpace(amt), 10, 64); err == nil {
			row.AmountMinor, row.Currency, row.HasAmount = v, cur, true
			row.Exponent = money.Exponent(cur)
			row.IsCredit = baseii.IsCreditTransactionCode(t.TransactionCode)
		}
	}
	return row
}

func recordDetailFromVisa(t baseii.Transaction, findings []baseii.Finding) RecordDetail {
	d := RecordDetail{RecordNumber: t.TransactionNumber}
	if t.ParseErr != nil {
		d.ParseErr = t.ParseErr.Error()
		return d
	}
	d.Identifier = t.TransactionCode
	if fn, ok := baseii.LookupFunctionName(t.TransactionCode); ok {
		d.Recognised = true
		d.Function = fn
	}
	for _, tcr := range t.TCRs {
		for _, f := range tcr.Fields {
			d.Fields = append(d.Fields, FieldRow{Tag: f.Tag, Value: maskValue(f.Tag, f.Value)})
		}
	}
	for _, f := range findings {
		d.Findings = append(d.Findings, GenericFinding{RecordNumber: f.TransactionNumber, Severity: string(f.Severity), Rule: f.Rule, Tag: f.Tag, Message: maskFindingMessage(f.Tag, f.Message)})
	}
	return d
}
