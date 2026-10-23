package integrationtest

import (
	"bytes"
	"fmt"
	"testing"

	"mciipm/internal/convert"
	"mciipm/internal/iso8583"
	"mciipm/internal/validate"
	"mciipm/internal/vbs"
)

// buildFileControl builds a File Header/1644 (function code 697) or File
// Trailer/1644 (695) carrying the DEs the manual marks mandatory for the
// customer-generated variant: DE 24, DE 48 (the PDS container) and DE 71.
func buildFileControl(functionCode string, pdsBlock string, messageNumber string) []byte {
	var bm iso8583.Bitmap
	bm.Set(1, true)
	bm.Set(24, true)
	bm.Set(48, true)
	bm.Set(71, true)

	raw := []byte("1644")
	raw = append(raw, bm.Bytes()...)
	raw = append(raw, []byte(buildFixed(functionCode))...)
	raw = append(raw, []byte(buildLLLVAR(pdsBlock))...)
	raw = append(raw, []byte(buildFixed(messageNumber))...)
	return raw
}

func fileHeader(messageNumber string) []byte {
	pds := buildPDS("0105", "TESTFILE0001") + buildPDS("0122", "P")
	return buildFileControl("697", pds, messageNumber)
}

// fileTrailer declares messageCount messages and amountChecksum as the
// DE 4 hash total. Passing all zeros for either is the manual's "not
// provided" convention.
func fileTrailer(messageNumber string, messageCount int, amountChecksum int64) []byte {
	pds := buildPDS("0105", "TESTFILE0001") +
		buildPDS("0301", fmt.Sprintf("%016d", amountChecksum)) +
		buildPDS("0306", fmt.Sprintf("%08d", messageCount))
	return buildFileControl("695", pds, messageNumber)
}

func TestNonNumericIPMControlTotalIsInvalid(t *testing.T) {
	for _, tag := range []string{"0301", "0306"} {
		t.Run(tag, func(t *testing.T) {
			trailer := buildFileControl("695", buildPDS(tag, "INVALID"), "00000002")
			summary := validate.Summarize(readRecords(t, fileHeader("00000001"), trailer), validate.Options{Role: validate.RoleOrg})
			for _, finding := range summary.Findings {
				if finding.Tag == "PDS"+tag && finding.Rule == "business.non_numeric_field" {
					return
				}
			}
			t.Fatalf("malformed total %s passed validation: %v", tag, summary.Findings)
		})
	}
}

// presentmentWithAmount is a compliant First Presentment with a specific
// DE 4 amount and DE 71 message number.
func presentmentWithAmount(messageNumber string, minorUnits int64) []byte {
	f := compliantFields()
	f[4] = buildFixed(fmt.Sprintf("%012d", minorUnits))
	f[71] = buildFixed(messageNumber)
	return buildFirstPresentment(f, nil, nil)
}

func readRecords(t *testing.T, records ...[]byte) []convert.RecordResult {
	t.Helper()
	var buf bytes.Buffer
	w := vbs.NewWriter(&buf)
	for _, rec := range records {
		if err := w.Write(rec); err != nil {
			t.Fatalf("vbs write: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("vbs close: %v", err)
	}
	results, err := convert.ReadAll(&buf, convert.Options{Encoding: iso8583.EncodingASCII})
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	for _, rr := range results {
		if rr.ParseErr != nil {
			t.Fatalf("record %d parse error: %v", rr.RecordNumber, rr.ParseErr)
		}
	}
	return results
}

func rules(findings []validate.Finding) []string {
	var out []string
	for _, f := range findings {
		out = append(out, f.Rule)
	}
	return out
}

func hasRule(findings []validate.Finding, rule string) bool {
	for _, f := range findings {
		if f.Rule == rule {
			return true
		}
	}
	return false
}

func TestFileControlTotals_Reconcile(t *testing.T) {
	// Header + two presentments + trailer = 4 messages, DE 4 sum 30000.
	results := readRecords(t,
		fileHeader("00000001"),
		presentmentWithAmount("00000002", 10000),
		presentmentWithAmount("00000003", 20000),
		fileTrailer("00000004", 4, 30000),
	)

	findings := validate.ValidateFile(results)
	if len(findings) != 0 {
		t.Fatalf("a file whose control totals agree produced findings: %v", rules(findings))
	}
}

func TestFileControlTotals_MessageCountMismatch(t *testing.T) {
	results := readRecords(t,
		fileHeader("00000001"),
		presentmentWithAmount("00000002", 10000),
		fileTrailer("00000003", 9, 10000), // declares 9, file holds 3
	)

	findings := validate.ValidateFile(results)
	if !hasRule(findings, "business.file_message_count") {
		t.Fatalf("declared message count of 9 against 3 read was not reported: %v", rules(findings))
	}
}

func TestFileControlTotals_AmountChecksumMismatch(t *testing.T) {
	results := readRecords(t,
		fileHeader("00000001"),
		presentmentWithAmount("00000002", 10000),
		fileTrailer("00000003", 3, 99999), // DE 4 sum is really 10000
	)

	findings := validate.ValidateFile(results)
	if !hasRule(findings, "business.file_amount_checksum") {
		t.Fatalf("declared DE 4 checksum of 99999 against 10000 read was not reported: %v", rules(findings))
	}
}

func TestFileControlTotals_AllZerosMeansNotProvided(t *testing.T) {
	// The manual lets originators zero-fill both control totals; a zero
	// value must not be reconciled against the real counts.
	results := readRecords(t,
		fileHeader("00000001"),
		presentmentWithAmount("00000002", 10000),
		fileTrailer("00000003", 0, 0),
	)

	findings := validate.ValidateFile(results)
	if len(findings) != 0 {
		t.Fatalf("zero-filled control totals were treated as real values: %v", rules(findings))
	}
}

func TestMessageNumberSequence(t *testing.T) {
	// Second presentment repeats message number 2.
	results := readRecords(t,
		fileHeader("00000001"),
		presentmentWithAmount("00000002", 10000),
		presentmentWithAmount("00000002", 20000),
		fileTrailer("00000004", 4, 30000),
	)

	findings := validate.ValidateFile(results)
	if !hasRule(findings, "business.message_number_sequence") {
		t.Fatalf("a repeated DE 71 value was not reported: %v", rules(findings))
	}
}

func TestMessageNumberNotResetInHeader(t *testing.T) {
	results := readRecords(t,
		fileHeader("00000007"),
		presentmentWithAmount("00000008", 10000),
		fileTrailer("00000009", 3, 10000),
	)

	findings := validate.ValidateFile(results)
	if !hasRule(findings, "business.message_number_reset") {
		t.Fatalf("a File Header with DE 71 = 7 was not reported: %v", rules(findings))
	}
}

func TestFileTrailerMissing(t *testing.T) {
	results := readRecords(t,
		fileHeader("00000001"),
		presentmentWithAmount("00000002", 10000),
	)

	findings := validate.ValidateFile(results)
	if !hasRule(findings, "structural.file_trailer_missing") {
		t.Fatalf("a file header with no trailer was not reported: %v", rules(findings))
	}
}

func TestFileRulesSkippedForExcerpt(t *testing.T) {
	// A handful of records cut out of a file carries no header or trailer;
	// that is an excerpt, not a malformed file.
	results := readRecords(t,
		presentmentWithAmount("00000050", 10000),
		presentmentWithAmount("00000051", 20000),
	)

	findings := validate.ValidateFile(results)
	if len(findings) != 0 {
		t.Fatalf("an excerpt with no control records was flagged: %v", rules(findings))
	}
}

// The File Header and File Trailer layouts are catalogued per side of the
// exchange (":ORG" customer-generated, ":DST" clearing-system-generated),
// so resolving them needs the validation role. Without that, the lookup
// missed and mandatory-field checks were skipped entirely for control
// records.
func TestFileControlMandatoryFieldsAreChecked(t *testing.T) {
	// A trailer missing its DE 48 container (and so PDS 0105/0301/0306).
	var bm iso8583.Bitmap
	bm.Set(1, true)
	bm.Set(24, true)
	bm.Set(71, true)
	raw := []byte("1644")
	raw = append(raw, bm.Bytes()...)
	raw = append(raw, []byte(buildFixed("695"))...)
	raw = append(raw, []byte(buildFixed("00000002"))...)

	results := readRecords(t, fileHeader("00000001"), raw)

	findings := validate.ValidateRecord(results[1], validate.Options{Role: validate.RoleOrg})
	if !hasRule(findings, "structural.missing_mandatory_field") {
		t.Fatalf("a File Trailer missing mandatory DE 48 was not reported: %v", rules(findings))
	}
	var sawDE48 bool
	for _, f := range findings {
		if f.Tag == "DE48" {
			sawDE48 = true
		}
	}
	if !sawDE48 {
		t.Fatalf("missing-field findings did not name DE48: %v", findings)
	}
}

func TestFlowSplitsDebitsFromCredits(t *testing.T) {
	// Same currency, opposite directions: DE 3 subfield 1 = 00 (Purchase,
	// a cardholder account debit) vs 20 (Credit/Purchase Return).
	debit := compliantFields()
	debit[71] = buildFixed("00000001")

	credit := compliantFields()
	credit[3] = buildFixed("203000")
	credit[4] = buildFixed("000000005000")
	credit[71] = buildFixed("00000002")

	results := readRecords(t,
		buildFirstPresentment(debit, nil, nil),
		buildFirstPresentment(credit, nil, nil),
	)

	s := validate.Summarize(results, validate.Options{Role: validate.RoleOrg})
	if len(s.AmountTotals) != 2 {
		t.Fatalf("got %d amount buckets, want 2 (one debit, one credit): %+v", len(s.AmountTotals), s.AmountTotals)
	}
	var debitTotal, creditTotal int64
	for _, ct := range s.AmountTotals {
		switch ct.Direction {
		case "debit":
			debitTotal = ct.MinorUnits
		case "credit":
			creditTotal = ct.MinorUnits
		}
	}
	if debitTotal != 10000 {
		t.Errorf("debit total = %d, want 10000", debitTotal)
	}
	if creditTotal != 5000 {
		t.Errorf("credit total = %d, want 5000", creditTotal)
	}
}
