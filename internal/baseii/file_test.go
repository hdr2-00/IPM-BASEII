package baseii

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// buildTrailer builds a TC 91 Batch Trailer or TC 92 File Trailer with
// the control totals at the positions the TC 50-92 manual documents.
func buildTrailer(tc string, transactions, tcrs int, sourceAmount, destAmount int64) []byte {
	return buildTrailerFull(tc, transactions, 0, tcrs, sourceAmount, destAmount)
}

// buildTrailerFull also sets Number of Monetary Transactions, which
// buildTrailer leaves zero-filled (the manual's "not provided").
func buildTrailerFull(tc string, transactions, monetary, tcrs int, sourceAmount, destAmount int64) []byte {
	b := blankTCR()
	posField(b, 1, 2, tc)
	posField(b, 3, 3, "0")
	posField(b, 4, 4, "0")
	posField(b, 5, 10, "123456")
	posField(b, 11, 15, "24350")
	posField(b, 16, 30, zeroPad(destAmount, 15))         // Destination Amount
	posField(b, 31, 42, zeroPad(int64(monetary), 12))    // Number of Monetary Transactions
	posField(b, 43, 48, "000001")                        // Batch Number
	posField(b, 49, 60, zeroPad(int64(tcrs), 12))        // Number of TCRs
	posField(b, 61, 66, "000000")                        // Reserved
	posField(b, 67, 74, "BATCH001")                      // Center Batch ID
	posField(b, 75, 83, zeroPad(int64(transactions), 9)) // Number of Transactions
	posField(b, 102, 116, zeroPad(sourceAmount, 15))     // Source Amount
	return b
}

func zeroPad(v int64, width int) string {
	s := ""
	neg := v < 0
	if neg {
		v = -v
	}
	for v > 0 {
		s = string(rune('0'+v%10)) + s
		v /= 10
	}
	if s == "" {
		s = "0"
	}
	for len(s) < width {
		s = "0" + s
	}
	return s
}

func groupLines(t *testing.T, records ...[]byte) []Transaction {
	t.Helper()
	var buf bytes.Buffer
	for _, rec := range records {
		buf.Write(rec)
		buf.WriteString("\r\n")
	}
	reader := NewReader(&buf, FormatCTF, FramingLines)
	txns, err := GroupTransactions(reader, EncodingASCII, 0)
	if err != nil {
		t.Fatalf("GroupTransactions: %v", err)
	}
	return txns
}

func TestTruncatedDraftIsInvalid(t *testing.T) {
	for _, raw := range [][]byte{[]byte("0500"), append(buildTCR0("4111111111111111", "000000010000", "TEST"), 'X')} {
		txns := groupLines(t, raw)
		findings := ValidateTransaction(txns[0])
		if !hasRuleNamed(findings, "structural.record_length") {
			t.Fatalf("wrong draft length passed validation: %v", findings)
		}
	}
	if findings := ValidateTransaction(groupLines(t, []byte("9000"))[0]); len(findings) != 0 {
		t.Fatalf("short control record rejected: %v", findings)
	}
}

func TestNonNumericTrailerTotalIsInvalid(t *testing.T) {
	raw := buildTrailer("91", 0, 0, 0, 0)
	posField(raw, 75, 83, "INVALID!!")
	summary := Summarize(groupLines(t, raw))
	if !hasRuleNamed(summary.Findings, "business.non_numeric_field") {
		t.Fatalf("malformed trailer total passed validation: %v", summary.Findings)
	}
}

func ruleNames(findings []Finding) []string {
	var out []string
	for _, f := range findings {
		out = append(out, f.Rule)
	}
	return out
}

func hasRuleNamed(findings []Finding, rule string) bool {
	for _, f := range findings {
		if f.Rule == rule {
			return true
		}
	}
	return false
}

func TestControlRecordsAreFieldDecoded(t *testing.T) {
	// A TC 91 is no longer raw text: the TC 50-92 manual's layout gives
	// it named fields.
	txns := groupLines(t, buildTrailer("91", 2, 3, 30000, 30000))
	if len(txns) != 1 {
		t.Fatalf("got %d transactions, want 1", len(txns))
	}
	tcr := txns[0].TCRs[0]
	if tcr.TCRType != "TC91" {
		t.Fatalf("TCRType = %q, want TC91", tcr.TCRType)
	}
	if len(tcr.Fields) == 0 {
		t.Fatal("control record was not field-decoded")
	}
	if v, ok := txns[0].Get("TC91.NumberOfTransactions"); !ok || strings.TrimLeft(v, "0") != "2" {
		t.Errorf("TC91.NumberOfTransactions = %q (present=%v), want 2", v, ok)
	}
	if v, ok := txns[0].Get("TC91.CenterBatchID"); !ok || v != "BATCH001" {
		t.Errorf("TC91.CenterBatchID = %q, want BATCH001", v)
	}
}

func TestControlTotals_Reconcile(t *testing.T) {
	// Two draft transactions over three TCRs, 20000 in each amount column.
	// The manual counts trailers too, so the batch trailer declares 3
	// transactions / 4 TCRs (the two drafts plus itself), and the file
	// trailer declares 4 / 5 (those plus the batch trailer and itself).
	txns := groupLines(t,
		buildTCR0("4111111111111111", "000000010000", "MERCHANT A"),
		buildTCR1(),
		buildTCR0("4012888888881881", "000000010000", "MERCHANT B"),
		buildTrailer("91", 3, 4, 20000, 20000),
		buildTrailer("92", 4, 5, 20000, 20000),
	)

	findings := ValidateFile(txns)
	if len(findings) != 0 {
		t.Fatalf("agreeing control totals produced findings: %v", ruleNames(findings))
	}
}

func TestControlTotals_TransactionCountMismatch(t *testing.T) {
	txns := groupLines(t,
		buildTCR0("4111111111111111", "000000010000", "MERCHANT A"),
		buildTrailer("91", 7, 1, 10000, 10000), // declares 7 transactions, file holds 1
	)

	findings := ValidateFile(txns)
	if !hasRuleNamed(findings, "business.control_transaction_count") {
		t.Fatalf("declared transaction count of 7 against 1 read was not reported: %v", ruleNames(findings))
	}
}

func TestControlTotals_TCRCountMismatch(t *testing.T) {
	txns := groupLines(t,
		buildTCR0("4111111111111111", "000000010000", "MERCHANT A"),
		buildTCR1(),
		buildTrailer("91", 1, 9, 10000, 10000), // declares 9 TCRs, file holds 2
	)

	findings := ValidateFile(txns)
	if !hasRuleNamed(findings, "business.control_tcr_count") {
		t.Fatalf("declared TCR count of 9 against 2 read was not reported: %v", ruleNames(findings))
	}
}

func TestControlTotals_AmountMismatch(t *testing.T) {
	txns := groupLines(t,
		buildTCR0("4111111111111111", "000000010000", "MERCHANT A"),
		buildTrailer("91", 2, 2, 99999, 99999), // counts agree; amounts are really 10000
	)

	findings := ValidateFile(txns)
	if !hasRuleNamed(findings, "business.control_source_amount") {
		t.Fatalf("source amount mismatch was not reported: %v", ruleNames(findings))
	}
	if !hasRuleNamed(findings, "business.control_destination_amount") {
		t.Fatalf("destination amount mismatch was not reported: %v", ruleNames(findings))
	}
}

func TestControlTotals_ZeroMeansNotPopulated(t *testing.T) {
	// Outgoing interchange requires these fields to be zero-filled; a
	// zero must not be reconciled against the real counts.
	txns := groupLines(t,
		buildTCR0("4111111111111111", "000000010000", "MERCHANT A"),
		buildTrailer("91", 0, 0, 0, 0),
	)

	findings := ValidateFile(txns)
	if len(findings) != 0 {
		t.Fatalf("zero-filled control totals were treated as real values: %v", ruleNames(findings))
	}
}

func TestControlTotals_PerBatchNotCumulative(t *testing.T) {
	// Two batches of one transaction each: the second batch trailer must
	// be checked against its own batch, not the running file total.
	txns := groupLines(t,
		buildTCR0("4111111111111111", "000000010000", "MERCHANT A"),
		buildTrailer("91", 2, 2, 10000, 10000), // one draft plus this trailer
		buildTCR0("4012888888881881", "000000010000", "MERCHANT B"),
		buildTrailer("91", 2, 2, 10000, 10000),
		buildTrailer("92", 5, 5, 20000, 20000), // two drafts, two batch trailers, itself
	)

	findings := ValidateFile(txns)
	if len(findings) != 0 {
		t.Fatalf("per-batch totals were reconciled cumulatively: %v", ruleNames(findings))
	}
}

func TestControlTotals_SkippedForExcerpt(t *testing.T) {
	txns := groupLines(t,
		buildTCR0("4111111111111111", "000000010000", "MERCHANT A"),
		buildTCR0("4012888888881881", "000000010000", "MERCHANT B"),
	)
	if findings := ValidateFile(txns); len(findings) != 0 {
		t.Fatalf("an excerpt with no trailers was flagged: %v", ruleNames(findings))
	}
}

// Regression: the counts include the trailer that carries them. Reading
// the exclusion the other way round - counting only the draft records -
// makes every real batch trailer in a production file miss by exactly
// one, which is how this was found.
func TestControlTotals_TrailerCountsItself(t *testing.T) {
	txns := groupLines(t,
		buildTCR0("4111111111111111", "000000010000", "MERCHANT A"),
		buildTCR0("4012888888881881", "000000010000", "MERCHANT B"),
		buildTrailer("91", 3, 3, 20000, 20000), // 2 drafts + this trailer
	)
	if findings := ValidateFile(txns); len(findings) != 0 {
		t.Fatalf("a trailer counting itself was reported as a mismatch: %v", ruleNames(findings))
	}

	// And the off-by-one is still caught when it is real.
	txns = groupLines(t,
		buildTCR0("4111111111111111", "000000010000", "MERCHANT A"),
		buildTCR0("4012888888881881", "000000010000", "MERCHANT B"),
		buildTrailer("91", 2, 2, 20000, 20000), // excludes itself: wrong
	)
	findings := ValidateFile(txns)
	if !hasRuleNamed(findings, "business.control_transaction_count") {
		t.Fatalf("a trailer that undercounts by one was not reported: %v", ruleNames(findings))
	}
}

// A record that failed to decode still occupies a slot the trailer
// counted, so it must count here too - otherwise one bad record makes an
// otherwise-correct trailer look wrong.
func TestControlTotals_UndecodableRecordStillCounts(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(buildTCR0("4111111111111111", "000000010000", "MERCHANT A"))
	buf.WriteString("\r\n")
	buf.Write([]byte("05X0" + strings.Repeat(" ", 164))) // TCR type "X": unrecognised
	buf.WriteString("\r\n")
	buf.Write(buildTrailer("91", 3, 3, 10000, 10000))
	buf.WriteString("\r\n")

	reader := NewReader(&buf, FormatCTF, FramingLines)
	txns, err := GroupTransactions(reader, EncodingASCII, 0)
	if err != nil {
		t.Fatalf("GroupTransactions: %v", err)
	}
	for _, f := range ValidateFile(txns) {
		if f.Rule == "business.control_transaction_count" || f.Rule == "business.control_tcr_count" {
			t.Fatalf("an undecodable record was left out of the count: %s", f.Message)
		}
	}
}

// Number of Monetary Transactions counts the draft transactions alone,
// where Number of Transactions counts those plus the trailers - the
// distinction a real file's two totals made visible.
func TestControlTotals_MonetaryExcludesTrailers(t *testing.T) {
	txns := groupLines(t,
		buildTCR0("4111111111111111", "000000010000", "MERCHANT A"),
		buildTCR0("4012888888881881", "000000010000", "MERCHANT B"),
		buildTrailerFull("91", 3, 2, 3, 20000, 20000), // 3 transactions, of which 2 monetary
	)
	if findings := ValidateFile(txns); len(findings) != 0 {
		t.Fatalf("correct monetary count was reported as a mismatch: %v", ruleNames(findings))
	}

	txns = groupLines(t,
		buildTCR0("4111111111111111", "000000010000", "MERCHANT A"),
		buildTCR0("4012888888881881", "000000010000", "MERCHANT B"),
		buildTrailerFull("91", 3, 3, 3, 20000, 20000), // counts itself as monetary: wrong
	)
	findings := ValidateFile(txns)
	if !hasRuleNamed(findings, "business.control_monetary_count") {
		t.Fatalf("a wrong monetary count was not reported: %v", ruleNames(findings))
	}
	for _, f := range findings {
		if f.Rule == "business.control_monetary_count" && f.Severity != SeverityWarning {
			t.Errorf("monetary count severity = %s, want WARNING (the rule is inferred, not specified)", f.Severity)
		}
	}
}

func TestGroupTransactions_TransactionCap(t *testing.T) {
	var buf bytes.Buffer
	for i := 0; i < 5; i++ {
		buf.Write(buildTCR0("4111111111111111", "000000010000", "MERCHANT"))
		buf.WriteString("\r\n")
	}
	reader := NewReader(&buf, FormatCTF, FramingLines)
	txns, err := GroupTransactions(reader, EncodingASCII, 2)
	if !errors.Is(err, ErrTooManyTransactions) {
		t.Fatalf("err = %v, want ErrTooManyTransactions", err)
	}
	if len(txns) > 2 {
		t.Fatalf("cap of 2 yielded %d transactions - the cap must stop the scan, not trim afterwards", len(txns))
	}
}

func TestGroupTransactions_AbortIsReported(t *testing.T) {
	// A fixed-block file whose last record is short: the reader cannot
	// know how to continue, and the caller must be told the scan stopped
	// rather than handed a silently short list.
	var buf bytes.Buffer
	buf.Write(buildTCR0("4111111111111111", "000000010000", "MERCHANT A"))
	buf.Write([]byte("05000000000"))

	reader := NewReader(&buf, FormatCTF, FramingFixed)
	txns, err := GroupTransactions(reader, EncodingASCII, 0)
	if !errors.Is(err, ErrScanAborted) {
		t.Fatalf("err = %v, want ErrScanAborted", err)
	}
	if len(txns) == 0 {
		t.Fatal("transactions read before the failure were discarded")
	}
}
