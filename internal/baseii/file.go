package baseii

import (
	"fmt"
	"strconv"
	"strings"
)

// File-level validation for BASE II: the control totals a TC 91 Batch
// Trailer and TC 92 File Trailer carry, per the "BASE II Clearing
// Interchange Formats, TC 50 to TC 92" manual (13 April 2019):
//
//   - Number of TCRs - count for the batch (TC 91) or the file (TC 92).
//     The manual is explicit: "The count includes all TCRs including
//     trailers. TC 90 header records are not included in this count." So
//     a trailer counts itself, and a file trailer's count also covers
//     every batch trailer in the file; only the optional TC 90 header is
//     left out.
//   - Number of Transactions - same rule: "The number includes all
//     transactions including trailers", counting logical transactions
//     rather than component records.
//   - Number of Monetary Transactions - the draft transactions alone,
//     with trailers excluded. The manual only says "the number of
//     monetary transactions" and never defines which Transaction Codes
//     are monetary, so this one is inferred rather than specified: in a
//     production file whose other totals all reconcile, both trailers
//     declared exactly the count of draft-data transactions, while their
//     Number of Transactions fields declared that count plus the
//     trailers. Every Transaction Code this catalog covers is financial
//     (sales drafts, credit vouchers, cash disbursements and their
//     dispute and reversal forms), so all of them count. Because the
//     definition is inferred from practice rather than read off the page,
//     a mismatch here is a warning, not an error.
//   - Source Amount - sum of transaction source amounts for the batch or
//     file, with two implied decimal places.
//   - Destination Amount - sum of transaction destination amounts.
//
// On outgoing interchange the manual requires several of these to be
// zero-filled, so a zero value means "not populated" and is skipped
// rather than reconciled - the same convention Mastercard's PDS
// 0301/0306 use.
//

// batchTotals accumulates what a trailer's control totals get checked
// against.
type batchTotals struct {
	transactions int // every record except the TC 90 header, trailers included
	monetary     int // draft-data transactions only, trailers excluded
	tcrs         int
	sourceAmount int64
	destAmount   int64
}

// count adds one record to the transaction and TCR counts. Every record
// except the TC 90 header counts, including trailers and including
// records that failed to decode - they still occupy a slot in the file
// the trailer is counting.
func (b *batchTotals) count(t Transaction) {
	b.transactions++
	b.tcrs += len(t.TCRs)
}

// addAmounts accumulates a transaction's own amounts. Trailers are
// excluded from this: the amount fields in a trailer are the declared
// totals being checked, not transaction amounts to add to them.
// countMonetary adds one money-moving transaction, which is every
// draft-data record and no control record.
func (b *batchTotals) countMonetary() { b.monetary++ }

func (b *batchTotals) addAmounts(t Transaction) {
	if v, ok := t.Get("TCR0.SourceAmount"); ok {
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			b.sourceAmount += n
		}
	}
	if v, ok := t.Get("TCR0.DestinationAmount"); ok {
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			b.destAmount += n
		}
	}
}

func (b *batchTotals) addTo(other *batchTotals) {
	other.transactions += b.transactions
	other.monetary += b.monetary
	other.tcrs += b.tcrs
	other.sourceAmount += b.sourceAmount
	other.destAmount += b.destAmount
}

// ValidateFile reconciles every batch and file trailer in a parsed file
// against the transactions actually read.
func ValidateFile(txns []Transaction) []Finding {
	var findings []Finding
	var batch, file batchTotals
	sawTrailer := false

	for _, t := range txns {
		switch t.TransactionCode {
		case "90":
			// Optional header; excluded from every count by the manual.
		case "91":
			sawTrailer = true
			// The trailer is part of the batch it closes, so it counts
			// itself before the comparison.
			batch.count(t)
			findings = append(findings, reconcileTrailer(t, &batch, "batch")...)
			// Rolling the closed batch forward carries its trailer into
			// the file total too, which is what "including trailers"
			// means for the file trailer's own count.
			batch.addTo(&file)
			batch = batchTotals{}
		case "92":
			sawTrailer = true
			// A file trailer closes any batch that never got its own
			// trailer, so whatever is still pending belongs to the file.
			batch.addTo(&file)
			batch = batchTotals{}
			file.count(t)
			findings = append(findings, reconcileTrailer(t, &file, "file")...)
			file = batchTotals{}
		default:
			batch.count(t)
			if t.ParseErr == nil {
				batch.countMonetary()
				batch.addAmounts(t)
			}
		}
	}

	if !sawTrailer {
		// An excerpt cut out of a file carries no trailers; that is not a
		// defect, and there is nothing to reconcile against.
		return nil
	}
	return findings
}

// reconcileTrailer compares one trailer's declared totals with what was
// read. scope is "batch" or "file", purely for the message wording.
func reconcileTrailer(trailer Transaction, totals *batchTotals, scope string) []Finding {
	var findings []Finding

	report := func(severity Severity, tag, label string, actual int64, rule string) {
		declared, ok := controlTotal(trailer, tag)
		if !ok || declared == actual {
			return
		}
		findings = append(findings, Finding{
			TransactionNumber: trailer.TransactionNumber,
			Severity:          severity,
			Rule:              rule,
			Tag:               tag,
			Message: fmt.Sprintf("%s trailer declares %s of %d but %d were read for this %s",
				scopeLabel(scope), label, declared, actual, scope),
		})
	}
	check := func(tag, label string, actual int64, rule string) {
		report(SeverityError, tag, label, actual, rule)
	}
	checkWarn := func(tag, label string, actual int64, rule string) {
		report(SeverityWarning, tag, label, actual, rule)
	}

	check(trailerTag(trailer, "Number of Transactions"), "a transaction count",
		int64(totals.transactions), "business.control_transaction_count")
	checkWarn(trailerTag(trailer, "Number of Monetary Transactions"), "a monetary transaction count",
		int64(totals.monetary), "business.control_monetary_count")
	check(trailerTag(trailer, "Number of TCRs"), "a TCR count",
		int64(totals.tcrs), "business.control_tcr_count")
	check(trailerTag(trailer, "Source Amount"), "a source amount total",
		totals.sourceAmount, "business.control_source_amount")
	check(trailerTag(trailer, "Destination Amount"), "a destination amount total",
		totals.destAmount, "business.control_destination_amount")

	return findings
}

func scopeLabel(scope string) string {
	if scope == "file" {
		return "File"
	}
	return "Batch"
}

// trailerTag builds the field tag a trailer's field carries, which
// depends on whether it is a TC 91 or a TC 92 record.
func trailerTag(trailer Transaction, name string) string {
	return fieldTag("TC"+trailer.TransactionCode, name)
}

// controlTotal reads one numeric control total from a trailer. A value of
// zero means "not populated" per the manual's outgoing-interchange rules,
// and a non-numeric value is left to the representation check.
func controlTotal(trailer Transaction, tag string) (int64, bool) {
	raw, ok := trailer.Get(tag)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || v == 0 {
		return 0, false
	}
	return v, true
}
