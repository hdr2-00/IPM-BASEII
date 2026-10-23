package validate

import (
	"fmt"
	"io"
	"sort"
	"strconv"

	"mciipm/internal/catalog"
	"mciipm/internal/convert"
	"mciipm/internal/money"
)

// CurrencyTotal accumulates a minor-unit amount total for one currency
// code and one money flow (see amountFlow). Totals are kept per flow
// rather than per currency alone because a presentment and a purchase
// return move money in opposite directions: summing both into one bucket
// produces a figure that is neither the gross nor the net, and cannot be
// reconciled against anything.
type CurrencyTotal struct {
	CurrencyCode string
	Function     string // "First Presentment", "First Chargeback (Full)", ...
	Direction    string // "debit", "credit", "inquiry", or a raw DE 3 code
	MinorUnits   int64
	Count        int
	// Exponent is where the decimal point sits for this currency: taken
	// from the message's own PDS 0148 (Currency Exponents) when it
	// carries one for this code, otherwise from the ISO 4217 table.
	Exponent int
}

// Display renders the accumulated total with its currency's own number
// of decimal places.
func (c CurrencyTotal) Display() string { return money.Format(c.MinorUnits, c.Exponent) }

// Flow names the money flow in one line, for the text report.
func (c CurrencyTotal) Flow() string {
	if c.Direction == "" {
		return c.Function
	}
	return c.Function + ", " + c.Direction
}

// IsCredit reports whether this flow moves money back to the cardholder.
func (c CurrencyTotal) IsCredit() bool { return c.Direction == "credit" }

// cardholderDirection maps DE 3 (Processing Code) subfield 1, Cardholder
// Transaction Type, to the direction the manual files it under: its value
// table groups the values under explicit "Cardholder Account Debits",
// "Cardholder Account Credits", and "Cardholder Account Inquiry"
// headings. Values absent from this map are reported by their raw code
// rather than guessed at.
var cardholderDirection = map[string]string{
	"00": "debit",   // Purchase (Goods and Services)
	"01": "debit",   // ATM Cash Withdrawal
	"09": "debit",   // Purchase with Cash Back
	"12": "debit",   // Cash Disbursement
	"17": "debit",   // Convenience Check
	"18": "debit",   // Unique Transaction
	"19": "debit",   // Fee Collection (Credit to Transaction Originator)
	"20": "credit",  // Credit (Purchase Return)
	"28": "credit",  // Payment Transaction
	"29": "credit",  // Fee Collection (Debit to Transaction Originator)
	"30": "inquiry", // Balance Inquiry
}

// CardholderDirection returns "debit", "credit", "inquiry", or "" for a
// DE 3 (Processing Code) value, using the manual's own grouping of
// Cardholder Transaction Type values. Exported so the dashboard can mark
// an amount with the same rule the summary buckets it by.
func CardholderDirection(processingCode string) string {
	if len(processingCode) < 2 {
		return ""
	}
	return cardholderDirection[processingCode[:2]]
}

// recordExponent resolves where the decimal point sits for a currency in
// one record: the message's own PDS 0148 (Currency Exponents) is
// authoritative when it covers the code, since the clearing system
// validates it and rejects mismatches; otherwise the ISO 4217 table
// applies.
func recordExponent(rr convert.RecordResult, currency string) int {
	if pds, ok := rr.Msg.Get("PDS0148"); ok {
		if exp, found := money.ParseExponents(pds)[currency]; found {
			return exp
		}
	}
	return money.Exponent(currency)
}

// amountFlow labels which money flow a record's DE 4 belongs to: its
// transaction function, and the cardholder-account direction from DE 3
// subfield 1.
func amountFlow(rr convert.RecordResult) (function, direction string) {
	function = "(unrecognised)"
	if rr.Classify.Recognised {
		function = rr.Classify.FunctionName
	}
	direction = "direction not stated"
	if pc, ok := rr.Msg.Get("DE3"); ok && len(pc) >= 2 {
		if d, known := cardholderDirection[pc[:2]]; known {
			direction = d
		} else {
			direction = "DE 3 type " + pc[:2]
		}
	}
	return function, direction
}

// Summary holds the full aggregated report for one validation run.
type Summary struct {
	TotalRecords      int
	ParseErrors       int
	RecordsByFunction map[string]int // "First Presentment" -> count, etc.
	RecordsByCategory map[string]int // "Presentment" -> count, etc.
	ErrorsByRule      map[string]int
	WarningsByRule    map[string]int
	TotalErrors       int
	TotalWarnings     int
	AmountTotals      map[string]*CurrencyTotal // keyed by currency code, from DE4 (transaction amount)
	Findings          []Finding
}

// Summarize processes every record once, producing both the full finding
// list and the aggregated summary.
func Summarize(results []convert.RecordResult, opt Options) Summary {
	s := Summary{
		RecordsByFunction: map[string]int{},
		RecordsByCategory: map[string]int{},
		ErrorsByRule:      map[string]int{},
		WarningsByRule:    map[string]int{},
		AmountTotals:      map[string]*CurrencyTotal{},
	}

	for _, rr := range results {
		s.TotalRecords++
		if rr.ParseErr != nil {
			s.ParseErrors++
		} else {
			cls := rr.Classify
			if cls.Recognised {
				s.RecordsByFunction[cls.FunctionName]++
				s.RecordsByCategory[cls.Category]++
			} else {
				s.RecordsByFunction["(unrecognised)"]++
				s.RecordsByCategory["(unrecognised)"]++
			}

			if amt, ok := rr.Msg.Get("DE4"); ok {
				cur, hasCur := rr.Msg.Get("DE49")
				if !hasCur {
					cur = "???"
				}
				if v, err := strconv.ParseInt(amt, 10, 64); err == nil {
					function, direction := amountFlow(rr)
					key := cur + "|" + function + "|" + direction
					ct, exists := s.AmountTotals[key]
					if !exists {
						ct = &CurrencyTotal{
							CurrencyCode: cur,
							Function:     function,
							Direction:    direction,
							Exponent:     recordExponent(rr, cur),
						}
						s.AmountTotals[key] = ct
					}
					ct.MinorUnits += v
					ct.Count++
				}
			}
		}

		s.tally(ValidateRecord(rr, opt))
	}

	// Whole-file rules (control totals, message-number continuity) run
	// after every record is known.
	s.tally(ValidateFile(results))

	return s
}

func (s *Summary) tally(findings []Finding) {
	s.Findings = append(s.Findings, findings...)
	for _, f := range findings {
		switch f.Severity {
		case SeverityError:
			s.TotalErrors++
			s.ErrorsByRule[f.Rule]++
		case SeverityWarning:
			s.TotalWarnings++
			s.WarningsByRule[f.Rule]++
		}
	}
}

// WriteReport writes a human-readable text summary report to w. maxFindings
// caps how many individual findings are listed (0 = unlimited).
func WriteReport(w io.Writer, s Summary, opt Options, maxFindings int) {
	fmt.Fprintf(w, "Mastercard IPM Clearing File Validation Report\n")
	fmt.Fprintf(w, "================================================\n\n")
	fmt.Fprintf(w, "Validated against: %s requirement column\n", roleLabel(opt.Role))
	fmt.Fprintf(w, "Total records read:     %d\n", s.TotalRecords)
	fmt.Fprintf(w, "Records that failed to parse: %d\n\n", s.ParseErrors)

	fmt.Fprintf(w, "Records by transaction function:\n")
	for _, k := range sortedKeys(s.RecordsByFunction) {
		fmt.Fprintf(w, "  %-45s %6d\n", k, s.RecordsByFunction[k])
	}
	fmt.Fprintf(w, "\nRecords by category:\n")
	for _, k := range sortedKeys(s.RecordsByCategory) {
		fmt.Fprintf(w, "  %-45s %6d\n", k, s.RecordsByCategory[k])
	}

	fmt.Fprintf(w, "\nAmount totals (DE4, Transaction Amount) by currency code and money flow:\n")
	fmt.Fprintf(w, "  (raw minor units as transmitted, displayed at the currency's own number of\n")
	fmt.Fprintf(w, "   decimal places - from the message's PDS 0148 where present, else ISO 4217.\n")
	fmt.Fprintf(w, "   Debits and credits are listed separately and are NOT netted here -\n")
	fmt.Fprintf(w, "   direction comes from DE 3 subfield 1, Cardholder Transaction Type.)\n")
	var curKeys []string
	for k := range s.AmountTotals {
		curKeys = append(curKeys, k)
	}
	sort.Strings(curKeys)
	for _, k := range curKeys {
		ct := s.AmountTotals[k]
		fmt.Fprintf(w, "  %-6s  %-48s  count=%-6d  minor_units=%-15d  %s (%d dp)\n",
			ct.CurrencyCode, ct.Flow(), ct.Count, ct.MinorUnits, ct.Display(), ct.Exponent)
	}

	fmt.Fprintf(w, "\nValidation errors: %d\n", s.TotalErrors)
	for _, k := range sortedKeys(s.ErrorsByRule) {
		fmt.Fprintf(w, "  %-40s %6d\n", k, s.ErrorsByRule[k])
	}
	fmt.Fprintf(w, "\nValidation warnings: %d\n", s.TotalWarnings)
	for _, k := range sortedKeys(s.WarningsByRule) {
		fmt.Fprintf(w, "  %-40s %6d\n", k, s.WarningsByRule[k])
	}

	if len(s.Findings) > 0 {
		fmt.Fprintf(w, "\nDetailed findings:\n")
		limit := len(s.Findings)
		truncated := false
		if maxFindings > 0 && limit > maxFindings {
			limit = maxFindings
			truncated = true
		}
		for _, f := range s.Findings[:limit] {
			fmt.Fprintf(w, "  %s\n", f.String())
		}
		if truncated {
			fmt.Fprintf(w, "  ... %d more findings not shown (increase -max-findings to see all)\n", len(s.Findings)-limit)
		}
	}
}

func roleLabel(r Role) string {
	if r == RoleDst {
		return "Destination (Dst) - validating a file received from Mastercard/the issuer side"
	}
	return "Originator (Org) - validating a file you are sending"
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var _ = catalog.Fields // keep import if unused in future edits
