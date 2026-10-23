package validate

import (
	"fmt"
	"strconv"
	"strings"

	"mciipm/internal/convert"
)

// File-level validation: rules that can only be checked by looking at a
// whole logical file rather than one record at a time. Per the manual's
// file structure rules and the PDS 0301/PDS 0306 definitions:
//
//   - A file must begin with a File Header/1644 and end with a File
//     Trailer/1644.
//   - Each message must contain DE 71 (Message Number) with a unique
//     value, greater than the previous message's, reset to 00000001 in
//     the File Header of each logical file.
//   - PDS 0306 (File Message Counts) contains the total count of messages
//     in the logical file, including the File Header and File Trailer.
//     Originators may provide all zeros; if it is non-zero the clearing
//     system validates it and the entire logical file rejects when the
//     counts do not match - hence ERROR, not WARNING.
//   - PDS 0301 (File Amount, Checksum) contains the sum of every
//     occurrence of DE 4 in the logical file as a hash total, ignoring
//     currency code and exponent entirely. Same all-zeros exemption and
//     same reject-on-mismatch consequence.
//
// Files containing no File Header and no File Trailer at all are treated
// as an excerpt of a file rather than a malformed one, and the structural
// file rules are skipped - operators routinely cut a handful of records
// out of a file to inspect them. The DE 71 sequence check still applies,
// since it holds within any run of consecutive messages.

const (
	fnFileHeader  = "697"
	fnFileTrailer = "695"
)

// logicalFile accumulates what a File Trailer's control totals get
// checked against.
type logicalFile struct {
	headerRecord int
	messageCount int
	de4Sum       int64
}

// ValidateFile runs the whole-file rules over every record of a parsed
// file, attributing each finding to the record that proves the problem:
// the trailer for a control-total mismatch, the offending message for a
// DE 71 break.
func ValidateFile(results []convert.RecordResult) []Finding {
	findings := messageNumberFindings(results)

	if !hasFileControlRecord(results) {
		return findings
	}

	if first := firstParsed(results); first != nil && !isFileControl(first, fnFileHeader) {
		findings = append(findings, Finding{
			RecordNumber: first.RecordNumber,
			Severity:     SeverityError,
			Rule:         "structural.file_header_missing",
			Message:      "the file carries File Header/Trailer messages but does not begin with a File Header/1644, which the manual requires",
		})
	}

	var lf *logicalFile
	for i := range results {
		rr := results[i]
		if rr.Msg == nil { // failed to parse, but still occupies a message slot
			if lf != nil {
				lf.messageCount++
			}
			continue
		}

		if isFileControl(&rr, fnFileHeader) {
			if lf != nil {
				findings = append(findings, unterminatedFile(lf))
			}
			lf = &logicalFile{headerRecord: rr.RecordNumber}
		}

		if lf != nil {
			lf.messageCount++
			if amt, ok := rr.Msg.Get("DE4"); ok {
				if v, err := strconv.ParseInt(strings.TrimSpace(amt), 10, 64); err == nil {
					lf.de4Sum += v
				}
			}
		}

		if isFileControl(&rr, fnFileTrailer) {
			if lf == nil {
				findings = append(findings, Finding{
					RecordNumber: rr.RecordNumber,
					Severity:     SeverityError,
					Rule:         "structural.file_trailer_without_header",
					Message:      "File Trailer/1644 with no preceding File Header/1644 in this file",
				})
				continue
			}
			findings = append(findings, reconcileTrailer(rr, lf)...)
			lf = nil
		}
	}
	if lf != nil {
		findings = append(findings, unterminatedFile(lf))
	}

	return findings
}

// reconcileTrailer checks a trailer's control totals against what was
// actually read out of the file.
func reconcileTrailer(rr convert.RecordResult, lf *logicalFile) []Finding {
	var findings []Finding

	if declared, ok := controlTotal(rr, "PDS0306"); ok && declared != int64(lf.messageCount) {
		findings = append(findings, Finding{
			RecordNumber: rr.RecordNumber,
			Severity:     SeverityError,
			Rule:         "business.file_message_count",
			Tag:          "PDS0306",
			Message: fmt.Sprintf("File Trailer declares %d messages in this logical file but %d were read (the count includes the File Header and File Trailer); the clearing system rejects the entire logical file on a mismatch",
				declared, lf.messageCount),
		})
	}

	if declared, ok := controlTotal(rr, "PDS0301"); ok && declared != lf.de4Sum {
		findings = append(findings, Finding{
			RecordNumber: rr.RecordNumber,
			Severity:     SeverityError,
			Rule:         "business.file_amount_checksum",
			Tag:          "PDS0301",
			Message: fmt.Sprintf("File Trailer declares a DE 4 hash total of %d but the sum of every DE 4 occurrence read was %d (currency code and exponent are ignored in this checksum); the clearing system rejects the entire logical file on a mismatch",
				declared, lf.de4Sum),
		})
	}

	return findings
}

// controlTotal reads a numeric control total from a trailer. An all-zeros
// value means "not provided" per the manual and is reported as absent, as
// is a non-numeric value (the per-record representation check reports
// that separately).
func controlTotal(rr convert.RecordResult, tag string) (int64, bool) {
	raw, ok := rr.Msg.Get(tag)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || v == 0 {
		return 0, false
	}
	return v, true
}

// messageNumberFindings checks DE 71's uniqueness, ordering, and its
// reset to 1 in each File Header.
func messageNumberFindings(results []convert.RecordResult) []Finding {
	var findings []Finding
	prev := int64(-1)
	prevRecord := 0

	for i := range results {
		rr := results[i]
		if rr.Msg == nil {
			continue
		}
		raw, ok := rr.Msg.Get("DE71")
		if !ok {
			continue // presence is the per-record mandatory-field check's job
		}
		n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			continue // non-numeric is reported by the representation check
		}

		if isFileControl(&rr, fnFileHeader) {
			if n != 1 {
				findings = append(findings, Finding{
					RecordNumber: rr.RecordNumber,
					Severity:     SeverityError,
					Rule:         "business.message_number_reset",
					Tag:          "DE71",
					Message:      fmt.Sprintf("DE 71 is %d in the File Header, but the manual requires it to be reset to 00000001 in the File Header of each logical file", n),
				})
			}
			prev, prevRecord = n, rr.RecordNumber
			continue
		}

		if prev >= 0 && n <= prev {
			findings = append(findings, Finding{
				RecordNumber: rr.RecordNumber,
				Severity:     SeverityError,
				Rule:         "business.message_number_sequence",
				Tag:          "DE71",
				Message: fmt.Sprintf("DE 71 is %d here but %d in the previous message (record %d); every message's Message Number must be unique and greater than the previous message's",
					n, prev, prevRecord),
			})
		}
		prev, prevRecord = n, rr.RecordNumber
	}
	return findings
}

func unterminatedFile(lf *logicalFile) Finding {
	return Finding{
		RecordNumber: lf.headerRecord,
		Severity:     SeverityError,
		Rule:         "structural.file_trailer_missing",
		Message:      fmt.Sprintf("the logical file opened by the File Header/1644 at record %d is never closed by a File Trailer/1644", lf.headerRecord),
	}
}

func isFileControl(rr *convert.RecordResult, functionCode string) bool {
	return rr.Msg != nil && rr.Msg.MTI == "1644" && rr.Msg.FunctionCode() == functionCode
}

func hasFileControlRecord(results []convert.RecordResult) bool {
	for i := range results {
		if isFileControl(&results[i], fnFileHeader) || isFileControl(&results[i], fnFileTrailer) {
			return true
		}
	}
	return false
}

func firstParsed(results []convert.RecordResult) *convert.RecordResult {
	for i := range results {
		if results[i].Msg != nil {
			return &results[i]
		}
	}
	return nil
}
