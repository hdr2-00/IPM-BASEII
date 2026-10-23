package integrationtest

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"mciipm/internal/convert"
	"mciipm/internal/iso8583"
	"mciipm/internal/validate"
	"mciipm/internal/vbs"
)

// buildField encodes one data element: for FIXED types just the value; for
// LLVAR/LLLVAR types, a zero-padded length prefix followed by the value.
func buildFixed(v string) string { return v }

func buildLLVAR(v string) string { return fmt.Sprintf("%02d%s", len(v), v) }

func buildLLLVAR(v string) string { return fmt.Sprintf("%03d%s", len(v), v) }

func buildPDS(tag, value string) string {
	return fmt.Sprintf("%s%03d%s", tag, len(value), value)
}

// buildFirstPresentment constructs a raw (pre-VBS-framing) First
// Presentment/1240 record with a caller-supplied set of DE values, so tests
// can both build a fully compliant message and deliberately omit/corrupt
// fields to exercise validator error paths.
func buildFirstPresentment(fields map[int]string, bitOverride *int, bitValue *bool) []byte {
	var bm iso8583.Bitmap
	bm.Set(1, true) // secondary bitmap present, per manual requirement
	for bit := range fields {
		bm.Set(bit, true)
	}
	if bitOverride != nil {
		bm.Set(*bitOverride, *bitValue)
	}

	var body strings.Builder
	// Fields must be written in ascending bit order.
	bits := []int{2, 3, 4, 12, 22, 24, 26, 31, 33, 43, 48, 49, 71, 94}
	for _, b := range bits {
		if v, ok := fields[b]; ok {
			body.WriteString(v)
		}
	}

	raw := []byte("1240")
	raw = append(raw, bm.Bytes()...)
	raw = append(raw, []byte(body.String())...)
	return raw
}

func compliantFields() map[int]string {
	pdsBlock := buildPDS("0148", "222") + buildPDS("0165", "1")
	return map[int]string{
		2:  buildLLVAR("5555555555554444"), // Luhn-valid Mastercard test PAN
		3:  buildFixed("003000"),
		4:  buildFixed("000000010000"),
		12: buildFixed("241215120000"),
		22: buildFixed("120100000000"),
		24: buildFixed("200"),
		26: buildFixed("5411"),
		31: buildLLVAR("12345678901234567890123"),
		33: buildLLVAR("123456"),
		43: buildLLVAR("TEST MERCHANT^CITY^US"),
		48: buildLLLVAR(pdsBlock),
		49: buildFixed("840"),
		71: buildFixed("00000001"),
		94: buildLLVAR("123456"),
	}
}

func TestFirstPresentmentRoundTrip_Compliant(t *testing.T) {
	raw := buildFirstPresentment(compliantFields(), nil, nil)

	msg, err := iso8583.ParseMessage(raw, iso8583.EncodingASCII)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}
	if msg.MTI != "1240" {
		t.Errorf("MTI = %q, want 1240", msg.MTI)
	}
	if pan, _ := msg.Get("DE2"); pan != "5555555555554444" {
		t.Errorf("DE2 = %q, want 5555555555554444", pan)
	}
	if v, _ := msg.Get("PDS0148"); v != "222" {
		t.Errorf("PDS0148 = %q, want 222", v)
	}
	if v, _ := msg.Get("PDS0165"); v != "1" {
		t.Errorf("PDS0165 = %q, want 1", v)
	}

	cls := iso8583.Classify(msg)
	if cls.Key != "1240:200" || !cls.Recognised {
		t.Fatalf("Classify = %+v, want key=1240:200 recognised=true", cls)
	}

	// Round-trip through VBS framing.
	var buf bytes.Buffer
	w := vbs.NewWriter(&buf)
	if err := w.Write(raw); err != nil {
		t.Fatalf("vbs write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("vbs close: %v", err)
	}

	results, err := convert.ReadAll(&buf, convert.Options{Encoding: iso8583.EncodingASCII})
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d records, want 1", len(results))
	}
	if results[0].ParseErr != nil {
		t.Fatalf("record parse error: %v", results[0].ParseErr)
	}

	findings := validate.ValidateRecord(results[0], validate.Options{Role: validate.RoleOrg})
	var errs []validate.Finding
	for _, f := range findings {
		if f.Severity == validate.SeverityError {
			errs = append(errs, f)
		}
	}
	if len(errs) != 0 {
		t.Fatalf("compliant message produced %d validation errors: %v", len(errs), errs)
	}

	// CSV output should contain the raw field numbers as headers.
	var csvBuf bytes.Buffer
	if err := convert.WriteCSV(&csvBuf, results, convert.Options{Encoding: iso8583.EncodingASCII}); err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}
	header := strings.Split(strings.Split(csvBuf.String(), "\n")[0], ",")
	wantFirst := "MTI"
	if header[0] != wantFirst {
		t.Errorf("CSV header[0] = %q, want %q", header[0], wantFirst)
	}
	if !strings.Contains(csvBuf.String(), "PDS0148") {
		t.Errorf("CSV output missing PDS0148 column; header=%v", header)
	}
	t.Logf("CSV output:\n%s", csvBuf.String())
}

func TestVBS_MissingTrailingTerminator(t *testing.T) {
	raw := buildFirstPresentment(compliantFields(), nil, nil)

	// Write two VBS records but deliberately skip the terminator (no
	// vbs.Writer.Close call), mirroring real-world files that just end
	// after the last record.
	var buf bytes.Buffer
	w := vbs.NewWriter(&buf)
	if err := w.Write(raw); err != nil {
		t.Fatalf("write record 1: %v", err)
	}
	if err := w.Write(raw); err != nil {
		t.Fatalf("write record 2: %v", err)
	}
	// no w.Close() - stream just ends here, like the OSV file that
	// triggered this bug report.

	results, err := convert.ReadAll(&buf, convert.Options{Encoding: iso8583.EncodingASCII})
	if err != nil {
		t.Fatalf("ReadAll should tolerate a missing VBS terminator, got error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d records, want 2", len(results))
	}
	for i, rr := range results {
		if rr.ParseErr != nil {
			t.Errorf("record %d: unexpected parse error: %v", i+1, rr.ParseErr)
		}
	}
}

func TestCSVFormulaInjectionSanitized(t *testing.T) {
	fields := compliantFields()
	// A card-acceptor-name value crafted to be interpreted as a live
	// formula by Excel/Sheets/LibreOffice if written unsanitized.
	fields[43] = buildLLVAR(`=HYPERLINK("http://evil.example/x","click")`)

	raw := buildFirstPresentment(fields, nil, nil)
	msg, err := iso8583.ParseMessage(raw, iso8583.EncodingASCII)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}
	rr := convert.RecordResult{RecordNumber: 1, Msg: msg, Classify: iso8583.Classify(msg)}

	var buf bytes.Buffer
	if err := convert.WriteCSV(&buf, []convert.RecordResult{rr}, convert.Options{Encoding: iso8583.EncodingASCII}); err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}
	csvOut := buf.String()
	if strings.Contains(csvOut, `"=HYPERLINK`) {
		t.Fatalf("formula payload was written unsanitized into CSV output:\n%s", csvOut)
	}
	if !strings.Contains(csvOut, `'=HYPERLINK`) {
		t.Fatalf("expected the formula payload to be neutralized with a leading quote, got:\n%s", csvOut)
	}
}

func TestFirstPresentment_MissingMandatoryField(t *testing.T) {
	fields := compliantFields()
	delete(fields, 33) // DE33 Forwarding Institution ID Code is Org-mandatory

	raw := buildFirstPresentment(fields, nil, nil)
	msg, err := iso8583.ParseMessage(raw, iso8583.EncodingASCII)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}

	rr := convert.RecordResult{RecordNumber: 1, Msg: msg, Classify: iso8583.Classify(msg)}
	findings := validate.ValidateRecord(rr, validate.Options{Role: validate.RoleOrg})

	found := false
	for _, f := range findings {
		if f.Rule == "structural.missing_mandatory_field" && f.Tag == "DE33" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a missing_mandatory_field finding for DE33, got: %v", findings)
	}
}

func TestFirstPresentment_BadLuhnPAN(t *testing.T) {
	fields := compliantFields()
	fields[2] = buildLLVAR("1234567890123456") // fails Luhn

	raw := buildFirstPresentment(fields, nil, nil)
	msg, err := iso8583.ParseMessage(raw, iso8583.EncodingASCII)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}
	rr := convert.RecordResult{RecordNumber: 1, Msg: msg, Classify: iso8583.Classify(msg)}
	findings := validate.ValidateRecord(rr, validate.Options{Role: validate.RoleOrg})

	found := false
	for _, f := range findings {
		if f.Rule == "business.luhn_check" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a luhn_check warning, got: %v", findings)
	}
}

func TestFirstPresentment_UnrecognisedFunctionCode(t *testing.T) {
	fields := compliantFields()
	fields[24] = buildFixed("999") // not a valid function code for MTI 1240

	raw := buildFirstPresentment(fields, nil, nil)
	msg, err := iso8583.ParseMessage(raw, iso8583.EncodingASCII)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}
	rr := convert.RecordResult{RecordNumber: 1, Msg: msg, Classify: iso8583.Classify(msg)}
	findings := validate.ValidateRecord(rr, validate.Options{Role: validate.RoleOrg})

	found := false
	for _, f := range findings {
		if f.Rule == "structural.unknown_function_code" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an unknown_function_code error, got: %v", findings)
	}
}

func TestEBCDICRoundTrip(t *testing.T) {
	raw := buildFirstPresentment(compliantFields(), nil, nil)
	ebcdicRaw := toEBCDIC(raw)

	msg, err := iso8583.ParseMessage(ebcdicRaw, iso8583.EncodingEBCDIC)
	if err != nil {
		t.Fatalf("ParseMessage (EBCDIC) failed: %v", err)
	}
	if pan, _ := msg.Get("DE2"); pan != "5555555555554444" {
		t.Errorf("DE2 = %q, want 5555555555554444", pan)
	}
	if v, _ := msg.Get("PDS0148"); v != "222" {
		t.Errorf("PDS0148 = %q, want 222", v)
	}
	if v, _ := msg.Get("PDS0165"); v != "1" {
		t.Errorf("PDS0165 = %q, want 1", v)
	}

	detected := iso8583.DetectEncoding(ebcdicRaw[:4])
	if detected != iso8583.EncodingEBCDIC {
		t.Errorf("DetectEncoding = %v, want EncodingEBCDIC", detected)
	}
}

// toEBCDIC converts everything except the binary bitmap bytes (offset 4-19)
// to EBCDIC, mirroring how a real mainframe-originated file would encode
// text fields while the bitmap remains raw binary.
func toEBCDIC(raw []byte) []byte {
	out := make([]byte, len(raw))
	copy(out, raw)
	// MTI
	asciiToEBCDIC(out[0:4])
	// bitmap (4:20) left as-is: it's binary, not text
	// everything after the bitmap is text in this synthetic test (no DE55)
	asciiToEBCDIC(out[20:])
	return out
}

func asciiToEBCDIC(b []byte) {
	table := map[byte]byte{}
	for i := 0; i < 10; i++ {
		table['0'+byte(i)] = 0xF0 + byte(i)
	}
	for i, c := range b {
		if v, ok := table[c]; ok {
			b[i] = v
		}
		// non-digit ASCII passthrough would need the full cp037 table; this
		// synthetic test only needs digits translated to prove the encoding
		// switch is honoured end-to-end for MTI + fixed numeric fields.
	}
}
