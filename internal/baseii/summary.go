package baseii

import (
	"fmt"
	"io"
	"sort"
	"strconv"

	"mciipm/internal/money"
)

// Summary holds the full aggregated report for one validation run.
type Summary struct {
	TotalTransactions int
	ParseErrors       int
	ByFunction        map[string]int // "Sales Draft" -> count
	ByCategory        map[string]int // "Presentment" -> count
	ErrorsByRule      map[string]int
	WarningsByRule    map[string]int
	TotalErrors       int
	TotalWarnings     int
	AmountTotals      map[string]*CurrencyTotal // keyed by Destination Currency Code
	Findings          []Finding
}

// CurrencyTotal accumulates a minor-unit amount total for one currency
// and one money flow (the Transaction Code's documented function, e.g.
// "Sales Draft" vs "Credit Voucher"). Totals are kept per flow rather
// than per currency alone because sales and credit vouchers move money in
// opposite directions: one combined figure is neither gross nor net and
// cannot be reconciled.
type CurrencyTotal struct {
	CurrencyCode string
	Function     string // "Sales Draft", "Credit Voucher", ...
	Direction    string // "debit" or "credit", from the Transaction Code
	MinorUnits   int64
	Count        int
	// Exponent is where this currency's decimal point sits. BASE II has
	// no per-message exponent field the way IPM's PDS 0148 does, so it
	// comes from ISO 4217.
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

// creditTransactionCodes are the Draft Data codes that move money back to
// the cardholder: the Credit Voucher and its dispute and reversal forms.
var creditTransactionCodes = map[string]bool{"06": true, "16": true, "26": true, "36": true}

// IsCreditTransactionCode reports whether a Transaction Code moves money
// back to the cardholder.
func IsCreditTransactionCode(code string) bool { return creditTransactionCodes[code] }

func Summarize(txns []Transaction) Summary {
	s := Summary{
		ByFunction:     map[string]int{},
		ByCategory:     map[string]int{},
		ErrorsByRule:   map[string]int{},
		WarningsByRule: map[string]int{},
		AmountTotals:   map[string]*CurrencyTotal{},
	}

	for _, t := range txns {
		s.TotalTransactions++
		if t.ParseErr != nil {
			s.ParseErrors++
		} else {
			name, cat := classifyTransaction(t)
			s.ByFunction[name]++
			s.ByCategory[cat]++

			// Destination Amount/Currency are only meaningfully populated
			// for cross-border transactions requiring currency conversion;
			// for domestic transactions they're commonly zero-filled, with
			// Source Amount/Currency (the transaction's own currency)
			// carrying the real value instead. Prefer Destination when it's
			// genuinely present (non-zero), otherwise fall back to Source,
			// so domestic-heavy files still get a meaningful total rather
			// than reporting everything as zero.
			amt, cur, ok := amountAndCurrency(t)
			if ok {
				if v, err := strconv.ParseInt(amt, 10, 64); err == nil {
					direction := "debit"
					if creditTransactionCodes[t.TransactionCode] {
						direction = "credit"
					}
					key := cur + "|" + name + "|" + direction
					ct, exists := s.AmountTotals[key]
					if !exists {
						ct = &CurrencyTotal{
							CurrencyCode: cur,
							Function:     name,
							Direction:    direction,
							Exponent:     money.Exponent(cur),
						}
						s.AmountTotals[key] = ct
					}
					ct.MinorUnits += v
					ct.Count++
				}
			}
		}

		s.tally(ValidateTransaction(t))
	}

	// Batch and file control totals can only be checked once every
	// transaction is known.
	s.tally(ValidateFile(txns))

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

// Amount exposes amountAndCurrency's choice of amount/currency pair for
// one transaction, for callers outside this package (the web dashboard's
// records table) that must not restate the Destination-else-Source rule.
func (t *Transaction) Amount() (amount, currency string, ok bool) {
	return amountAndCurrency(*t)
}

// amountAndCurrency picks the most meaningful amount/currency pair for a
// transaction: Destination Amount/Currency when genuinely populated
// (non-zero - i.e. a cross-border conversion occurred), otherwise Source
// Amount/Currency (the transaction's own currency, which real-world
// domestic-only files populate far more reliably than Destination).
func amountAndCurrency(t Transaction) (amount, currency string, ok bool) {
	destAmt, destOK := t.Get("TCR0.DestinationAmount")
	destCur, _ := t.Get("TCR0.DestinationCurrencyCode")
	if destOK && !isZeroAmount(destAmt) && destCur != "" {
		return destAmt, destCur, true
	}
	srcAmt, srcOK := t.Get("TCR0.SourceAmount")
	srcCur, hasSrcCur := t.Get("TCR0.SourceCurrencyCode")
	if srcOK {
		if !hasSrcCur || srcCur == "" {
			srcCur = "???"
		}
		return srcAmt, srcCur, true
	}
	return "", "", false
}

func isZeroAmount(s string) bool {
	for _, c := range s {
		if c != '0' {
			return false
		}
	}
	return true
}

func classifyTransaction(t Transaction) (name, category string) {
	if fn, ok := lookupFunctionName(t.TransactionCode); ok {
		return fn.Name, fn.Category
	}
	return "(unrecognised)", "(unrecognised)"
}

// WriteReport writes a human-readable text summary report to w.
func WriteReport(w io.Writer, s Summary, maxFindings int) {
	fmt.Fprintf(w, "Visa BASE II Draft Data (TC 05 family) Validation Report\n")
	fmt.Fprintf(w, "==========================================================\n\n")
	fmt.Fprintf(w, "Total transactions read:      %d\n", s.TotalTransactions)
	fmt.Fprintf(w, "Transactions that failed to parse: %d\n\n", s.ParseErrors)

	fmt.Fprintf(w, "Transactions by function:\n")
	for _, k := range sortedKeys(s.ByFunction) {
		fmt.Fprintf(w, "  %-45s %6d\n", k, s.ByFunction[k])
	}
	fmt.Fprintf(w, "\nTransactions by category:\n")
	for _, k := range sortedKeys(s.ByCategory) {
		fmt.Fprintf(w, "  %-45s %6d\n", k, s.ByCategory[k])
	}

	fmt.Fprintf(w, "\nAmount totals (Destination Amount if present, else Source Amount) by currency code\n")
	fmt.Fprintf(w, "and transaction function. Sales and credit vouchers are listed separately and\n")
	fmt.Fprintf(w, "are NOT netted here:\n")
	var curKeys []string
	for k := range s.AmountTotals {
		curKeys = append(curKeys, k)
	}
	sort.Strings(curKeys)
	for _, k := range curKeys {
		ct := s.AmountTotals[k]
		fmt.Fprintf(w, "  %-6s  %-42s  count=%-6d  minor_units=%-15d  %s (%d dp)\n",
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
			fmt.Fprintf(w, "  ... %d more findings not shown\n", len(s.Findings)-limit)
		}
	}
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
