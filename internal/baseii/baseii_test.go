package baseii

import (
	"bytes"
	"strings"
	"testing"
)

// posField writes value into buf at 1-indexed [start,end], left-justified
// and space-padded (or right-justified zero-padded for numeric-looking
// short values, kept simple here: callers pass exactly the right length).
func posField(buf []byte, start, end int, value string) {
	width := end - start + 1
	if len(value) > width {
		value = value[:width]
	}
	for len(value) < width {
		value = value + " "
	}
	copy(buf[start-1:end], value)
}

func blankTCR() []byte {
	b := make([]byte, 168)
	for i := range b {
		b[i] = ' '
	}
	return b
}

func buildTCR0(accountNumber, destAmount, merchantName string) []byte {
	b := blankTCR()
	posField(b, 1, 2, "05")
	posField(b, 3, 3, "0")
	posField(b, 4, 4, "0")
	posField(b, 5, 20, accountNumber)
	posField(b, 21, 23, "000")
	posField(b, 24, 24, "0")
	posField(b, 25, 25, " ")
	posField(b, 26, 26, " ")
	posField(b, 27, 49, "12345678901234567890123")
	posField(b, 50, 57, "12345678")
	posField(b, 58, 61, "0615")
	posField(b, 62, 73, destAmount)
	posField(b, 74, 76, "840")
	posField(b, 77, 88, destAmount)
	posField(b, 89, 91, "840")
	posField(b, 92, 116, merchantName)
	posField(b, 117, 129, "SAN FRANCISCO")
	posField(b, 130, 132, "US ")
	posField(b, 133, 136, "5411")
	posField(b, 137, 141, "94105")
	posField(b, 142, 144, "CA ")
	posField(b, 145, 145, "0")
	posField(b, 146, 146, "1")
	posField(b, 147, 147, "0")
	posField(b, 148, 149, "00")
	posField(b, 150, 150, "0")
	posField(b, 151, 151, "0")
	posField(b, 152, 157, "AUTH01")
	posField(b, 158, 158, "0")
	posField(b, 159, 159, " ")
	posField(b, 160, 160, "0")
	posField(b, 161, 161, "0")
	posField(b, 162, 163, "05")
	posField(b, 164, 167, "2166")
	posField(b, 168, 168, "0")
	return b
}

func buildTCR1() []byte {
	b := blankTCR()
	posField(b, 1, 2, "05")
	posField(b, 3, 3, "0")
	posField(b, 4, 4, "1")
	posField(b, 5, 5, "0")
	return b
}

func TestLineDelimitedFraming_MixedLengthRecords(t *testing.T) {
	// Mirrors a real production file: TC05-family TCRs are a full 168
	// bytes, but control records (90/91/92) are shorter - 161 and 162
	// bytes here - all CRLF-terminated. A fixed-block reader that ignores
	// the line endings would drift out of alignment the moment it hits
	// the first short record; DetectFraming + FramingLines must not.
	tc90 := blankTCR()
	posField(tc90, 1, 2, "90")
	posField(tc90, 4, 4, "0")
	// TC90 (File Header) was observed at full 168 bytes in the real file;
	// only the trailer records were shorter.

	tc05 := buildTCR0("4255691003152451", "000001225000", "GYM AND DINE")
	tc1 := buildTCR1()

	tc91 := blankTCR()
	posField(tc91, 1, 2, "91")
	posField(tc91, 4, 4, "0")
	tc91Short := tc91[:161]

	tc92 := blankTCR()
	posField(tc92, 1, 2, "92")
	posField(tc92, 4, 4, "0")
	tc92Short := tc92[:162]

	var buf bytes.Buffer
	for _, rec := range [][]byte{tc90, tc05, tc1, tc91Short, tc92Short} {
		buf.Write(rec)
		buf.WriteString("\r\n")
	}

	sample := buf.Bytes()
	framing := DetectFraming(sample[:min(len(sample), 176)], FormatCTF)
	if framing != FramingLines {
		t.Fatalf("DetectFraming = %v, want FramingLines", framing)
	}

	reader := NewReader(bytes.NewReader(sample), FormatCTF, framing)
	txns, err := GroupTransactions(reader, EncodingASCII, 0)
	if err != nil {
		t.Fatalf("GroupTransactions: %v", err)
	}

	// Expect: TC90 (standalone), TC05 (TCR0+TCR1 grouped), TC91, TC92 = 4 transactions.
	if len(txns) != 4 {
		t.Fatalf("got %d transactions, want 4: %+v", len(txns), txns)
	}
	if txns[0].TransactionCode != "90" {
		t.Errorf("txn 1 = %q, want 90", txns[0].TransactionCode)
	}
	if txns[1].TransactionCode != "05" || len(txns[1].TCRs) != 2 {
		t.Fatalf("txn 2 = %+v, want TC05 with 2 TCRs (TCR0+TCR1)", txns[1])
	}
	if txns[2].TransactionCode != "91" || txns[3].TransactionCode != "92" {
		t.Errorf("txns 3,4 = %q, %q, want 91, 92", txns[2].TransactionCode, txns[3].TransactionCode)
	}

	// Critically: no CR/LF bytes should have leaked into any decoded field
	// (this is exactly what the original bug produced).
	acct, ok := txns[1].Get("TCR0.AccountNumber")
	if !ok || acct != "4255691003152451" {
		t.Errorf("TCR0.AccountNumber = %q, ok=%v, want clean 4255691003152451", acct, ok)
	}
	if strings.ContainsAny(acct, "\r\n") {
		t.Errorf("TCR0.AccountNumber contains a line-ending byte: %q", acct)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestDetectFraming_FixedBlockUnaffected(t *testing.T) {
	// A genuine no-delimiter fixed-block file must still be detected as
	// FramingFixed (no false positive from coincidental byte values).
	var buf bytes.Buffer
	buf.Write(buildTCR0("4111111111111111", "000000010000", "TEST MERCHANT"))
	buf.Write(buildTCR1())
	framing := DetectFraming(buf.Bytes(), FormatCTF)
	if framing != FramingFixed {
		t.Errorf("DetectFraming = %v, want FramingFixed for a genuine fixed-block stream", framing)
	}
}

func TestGroupTransactions_TC05_TCR0_TCR1(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(buildTCR0("4111111111111111", "000000010000", "TEST MERCHANT"))
	buf.Write(buildTCR1())

	reader := NewReader(&buf, FormatCTF, FramingFixed)
	txns, err := GroupTransactions(reader, EncodingASCII, 0)
	if err != nil {
		t.Fatalf("GroupTransactions: %v", err)
	}
	if len(txns) != 1 {
		t.Fatalf("got %d transactions, want 1", len(txns))
	}
	txn := txns[0]
	if txn.TransactionCode != "05" {
		t.Errorf("TransactionCode = %q, want 05", txn.TransactionCode)
	}
	if len(txn.TCRs) != 2 {
		t.Fatalf("got %d TCRs, want 2 (TCR0+TCR1)", len(txn.TCRs))
	}
	if txn.TCRs[0].TCRType != "TCR0" || txn.TCRs[1].TCRType != "TCR1" {
		t.Errorf("TCR types = %q, %q, want TCR0, TCR1", txn.TCRs[0].TCRType, txn.TCRs[1].TCRType)
	}

	acct, ok := txn.Get("TCR0.AccountNumber")
	if !ok || acct != "4111111111111111" {
		t.Errorf("TCR0.AccountNumber = %q, ok=%v, want 4111111111111111", acct, ok)
	}
	amt, ok := txn.Get("TCR0.DestinationAmount")
	if !ok || amt != "000000010000" {
		t.Errorf("TCR0.DestinationAmount = %q, ok=%v", amt, ok)
	}
	name, ok := txn.Get("TCR0.MerchantName")
	if !ok || !strings.Contains(name, "TEST MERCHANT") {
		t.Errorf("TCR0.MerchantName = %q, ok=%v", name, ok)
	}
}

func TestGroupTransactions_MultipleTransactionsAndControlRecords(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(buildTCR0("4111111111111111", "000000010000", "MERCHANT ONE"))
	buf.Write(buildTCR0("5555555555554444", "000000020000", "MERCHANT TWO")) // new txn: TCR seq 0 again, not > previous 0

	// a control record (TC 90) should never merge with neighbouring draft data
	ctrl := blankTCR()
	posField(ctrl, 1, 2, "90")
	posField(ctrl, 4, 4, "0")
	buf.Write(ctrl)

	buf.Write(buildTCR0("4111111111111111", "000000030000", "MERCHANT THREE"))

	reader := NewReader(&buf, FormatCTF, FramingFixed)
	txns, err := GroupTransactions(reader, EncodingASCII, 0)
	if err != nil {
		t.Fatalf("GroupTransactions: %v", err)
	}
	if len(txns) != 4 {
		t.Fatalf("got %d transactions, want 4 (txn, txn, control, txn), got: %+v", len(txns), txns)
	}
	if txns[2].TransactionCode != "90" || len(txns[2].TCRs) != 1 {
		t.Errorf("transaction 3 = %+v, want a standalone TC90 control record", txns[2])
	}
}

func TestDetectEncoding(t *testing.T) {
	asciiRec := []byte("05")
	if got := DetectEncoding(asciiRec); got != EncodingASCII {
		t.Errorf("DetectEncoding(ASCII '05') = %v, want ascii", got)
	}
	ebcdicRec := []byte{0xF0, 0xF5} // EBCDIC "05"
	if got := DetectEncoding(ebcdicRec); got != EncodingEBCDIC {
		t.Errorf("DetectEncoding(EBCDIC '05') = %v, want ebcdic", got)
	}
}

func TestValidate_CompliantTransaction_NoFindings(t *testing.T) {
	var buf bytes.Buffer
	// 4111111111111111 is a standard Luhn-valid test PAN.
	buf.Write(buildTCR0("4111111111111111", "000000010000", "TEST MERCHANT"))
	buf.Write(buildTCR1())

	reader := NewReader(&buf, FormatCTF, FramingFixed)
	txns, err := GroupTransactions(reader, EncodingASCII, 0)
	if err != nil {
		t.Fatalf("GroupTransactions: %v", err)
	}
	findings := ValidateTransaction(txns[0])
	var errs []Finding
	for _, f := range findings {
		if f.Severity == SeverityError {
			errs = append(errs, f)
		}
	}
	if len(errs) != 0 {
		t.Fatalf("compliant transaction produced %d validation errors: %v", len(errs), errs)
	}
}

func TestValidate_BadModulus10_Warning(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(buildTCR0("1234567890123456", "000000010000", "TEST MERCHANT")) // fails Luhn
	reader := NewReader(&buf, FormatCTF, FramingFixed)
	txns, _ := GroupTransactions(reader, EncodingASCII, 0)
	findings := ValidateTransaction(txns[0])

	found := false
	for _, f := range findings {
		if f.Rule == "business.modulus10_check" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a modulus10_check warning, got: %v", findings)
	}
}

func TestValidate_BadDate_Error(t *testing.T) {
	b := buildTCR0("4111111111111111", "000000010000", "TEST MERCHANT")
	posField(b, 58, 61, "1345") // month 13 invalid
	var buf bytes.Buffer
	buf.Write(b)
	reader := NewReader(&buf, FormatCTF, FramingFixed)
	txns, _ := GroupTransactions(reader, EncodingASCII, 0)
	findings := ValidateTransaction(txns[0])

	found := false
	for _, f := range findings {
		if f.Rule == "business.invalid_date" && f.Tag == "TCR0.PurchaseDateMMDD" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an invalid_date error for PurchaseDateMMDD, got: %v", findings)
	}
}

func TestConvert_CSVFormulaInjectionSanitized(t *testing.T) {
	b := buildTCR0("4111111111111111", "000000010000", `=HYPERLINK("http://evil.example")`)
	var buf bytes.Buffer
	buf.Write(b)
	reader := NewReader(&buf, FormatCTF, FramingFixed)
	txns, _ := GroupTransactions(reader, EncodingASCII, 0)

	var out bytes.Buffer
	if err := WriteCSV(&out, txns, true); err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}
	csvOut := out.String()
	if strings.Contains(csvOut, `"=HYPERLINK`) {
		t.Fatalf("formula payload written unsanitized:\n%s", csvOut)
	}
	if !strings.Contains(csvOut, `'=HYPERLINK`) {
		t.Fatalf("expected formula payload neutralized with leading quote, got:\n%s", csvOut)
	}
}
