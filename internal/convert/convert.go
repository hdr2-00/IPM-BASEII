// Package convert flattens parsed Mastercard IPM presentment/addendum/
// chargeback messages into a single CSV, with raw DE/PDS field numbers as
// column headers (e.g. "DE2", "PDS0501") rather than descriptive names.
package convert

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"mciipm/internal/catalog"
	"mciipm/internal/iso8583"
	"mciipm/internal/vbs"
)

// RecordResult holds one parsed (or failed) record plus its classification,
// used by both the converter and the validator so the file only needs to be
// read and parsed once per tool run.
type RecordResult struct {
	RecordNumber int
	Raw          []byte
	Msg          *iso8583.Message
	Classify     iso8583.ClassifyResult
	ParseErr     error
}

// Options controls how a file is read and which records are included.
type Options struct {
	Encoding    iso8583.Encoding
	Blocked1014 bool
	IncludeAll  bool // include file header/trailer/other non-presentment/chargeback records too

	// MaxRecords bounds parsed object count independently of file size.
	// Zero means unlimited; network callers should set a limit.
	MaxRecords int

	// KeepRaw retains each record's raw bytes on its RecordResult. Only
	// encoding detection needs them, and only for the first record, so
	// this defaults to off: holding them for every record keeps a second
	// copy of the whole file in memory for as long as the results live.
	KeepRaw bool
}

// ErrTooManyRecords is returned by ReadAll when Options.MaxRecords is
// exceeded.
var ErrTooManyRecords = errors.New("convert: record count exceeds configured maximum")

// ReadAll reads every VBS record from r, parsing each as an IPM message. It
// never stops early on a per-record parse error - failures are recorded in
// RecordResult.ParseErr so the caller (validator) can report them and move
// on to the next record. It does stop early - returning ErrTooManyRecords -
// if Options.MaxRecords is set and exceeded.
func ReadAll(r io.Reader, opt Options) ([]RecordResult, error) {
	reader, err := vbs.NewReader(r, opt.Blocked1014)
	if err != nil {
		return nil, fmt.Errorf("opening VBS stream: %w", err)
	}

	var results []RecordResult
	recNum := 0
	for {
		if opt.MaxRecords > 0 && recNum >= opt.MaxRecords {
			return results, fmt.Errorf("%w (%d)", ErrTooManyRecords, opt.MaxRecords)
		}
		raw, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return results, fmt.Errorf("reading record %d: %w", recNum+1, err)
		}
		recNum++

		msg, perr := iso8583.ParseMessage(raw, opt.Encoding)
		rr := RecordResult{RecordNumber: recNum, ParseErr: perr}
		if opt.KeepRaw {
			rr.Raw = raw
		}
		if perr == nil {
			rr.Msg = msg
			rr.Classify = iso8583.Classify(msg)
		}
		results = append(results, rr)
	}
	return results, nil
}

// InScope reports whether a record should appear in the flattened CSV given
// the current options (presentment/chargeback/addendum only, unless
// IncludeAll is set).
func InScope(rr RecordResult, opt Options) bool {
	if rr.Msg == nil {
		return false
	}
	if opt.IncludeAll {
		return true
	}
	return catalog.IsPresentmentOrChargeback(rr.Msg.MTI, rr.Msg.FunctionCode())
}

// BuildHeader computes the sorted column header (MTI, then DEs ascending,
// then PDSs ascending) from the union of tags across all in-scope records.
func BuildHeader(results []RecordResult, opt Options) []string {
	seen := map[string]bool{}
	for _, rr := range results {
		if !InScope(rr, opt) {
			continue
		}
		for _, f := range rr.Msg.Fields {
			seen[f.Tag] = true
		}
	}

	var des, pdss []string
	hasMTI := seen["MTI"]
	for tag := range seen {
		switch {
		case tag == "MTI":
			// handled separately
		case strings.HasPrefix(tag, "DE"):
			des = append(des, tag)
		case strings.HasPrefix(tag, "PDS"):
			pdss = append(pdss, tag)
		}
	}
	sort.Slice(des, func(i, j int) bool { return numericSuffix(des[i], "DE") < numericSuffix(des[j], "DE") })
	sort.Slice(pdss, func(i, j int) bool { return numericSuffix(pdss[i], "PDS") < numericSuffix(pdss[j], "PDS") })

	header := make([]string, 0, len(des)+len(pdss)+1)
	if hasMTI {
		header = append(header, "MTI")
	}
	header = append(header, des...)
	header = append(header, pdss...)
	return header
}

func numericSuffix(tag, prefix string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(tag, prefix))
	if err != nil {
		return 1 << 30 // unparsable suffixes sort last
	}
	return n
}

// formulaTriggerChars are the leading characters spreadsheet applications
// treat as the start of a formula (per OWASP CSV Injection guidance).
const formulaTriggerChars = "=+-@\t\r"

// sanitizeCSVField neutralizes CSV/formula injection: if the value starts
// with a character a spreadsheet application would interpret as launching a
// formula, prefix it with a single quote so it's opened as literal text
// instead. This does not change the value as seen by ipmvalidate or the web
// UI's record detail view (both work from the parsed Message directly) -
// it only affects what's written into the CSV file itself.
func sanitizeCSVField(v string) string {
	if v == "" {
		return v
	}
	if strings.ContainsRune(formulaTriggerChars, rune(v[0])) {
		return "'" + v
	}
	return v
}

// WriteCSV exports in-scope records, escaping leading formula characters.
func WriteCSV(w io.Writer, results []RecordResult, opt Options) error {
	header := BuildHeader(results, opt)
	cw := csv.NewWriter(w)
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, rr := range results {
		if !InScope(rr, opt) {
			continue
		}
		row := make([]string, len(header))
		for i, tag := range header {
			row[i] = sanitizeCSVField(rr.Msg.Values[tag])
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
