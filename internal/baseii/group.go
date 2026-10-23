package baseii

import (
	"errors"
	"fmt"
	"io"

	"mciipm/internal/baseii/catalog"
)

// ErrScanAborted reports that the file could not be read to the end, so
// the transactions returned are a prefix of an unknown whole.
var ErrScanAborted = errors.New("baseii: scan aborted before end of file")

// ErrTooManyTransactions reports that the configured transaction cap was
// reached before the end of the file.
var ErrTooManyTransactions = errors.New("baseii: transaction count exceeds configured maximum")

// GroupTransactions reads every TCR from reader and groups them into
// Transactions per the manual's own boundary rule (Chapter 1, "BASE II
// Center Transaction Files"): a transaction ends - and the next begins -
// when either the Transaction Code changes, or the Transaction Component
// Sequence Number is not strictly greater than the previous TCR's (using
// the TCR0..TCR7,TCRD,TCRE ordinal sequence implied by their documented
// order). Control records (Transaction Codes outside the Draft Data
// family, e.g. 90/91/92 file/batch header/trailer records) are never
// grouped with neighbouring records - each is its own single-TCR
// "transaction", matching the CTF structure diagram, which shows them as
// standalone records rather than part of a multi-TCR set.
//
// A record read failure does not discard what was already read: the
// transactions parsed so far are returned together with ErrScanAborted,
// so a caller can show them while still telling the operator that the
// rest of the file was never scanned. Silently returning a short list
// with no error reads as "this file has 500 transactions" when the truth
// is "the first 500 of an unknown number".
//
// maxTransactions, if non-zero, stops the scan once that many
// transactions have been grouped and returns ErrTooManyTransactions.
// Callers taking untrusted input over a network must set it: a file of
// minimal-length records yields transactions far out of proportion to
// its byte size.
func GroupTransactions(reader *Reader, enc Encoding, maxTransactions int) ([]Transaction, error) {
	var transactions []Transaction
	var current *Transaction
	lastOrdinal := -1
	txnNum := 0

	flush := func() {
		if current != nil {
			transactions = append(transactions, *current)
			current = nil
		}
	}

	for {
		raw, err := reader.Next()
		if errors.Is(err, io.EOF) {
			flush()
			return transactions, nil
		}
		if err != nil {
			flush()
			txnNum++
			transactions = append(transactions, Transaction{
				TransactionNumber: txnNum,
				ParseErr:          fmt.Errorf("reading record %d: %w", reader.RecordNumber, err),
			})
			return transactions, fmt.Errorf("%w at record %d: %v", ErrScanAborted, reader.RecordNumber, err)
		}

		if maxTransactions > 0 && txnNum >= maxTransactions {
			flush()
			return transactions, fmt.Errorf("%w (%d)", ErrTooManyTransactions, maxTransactions)
		}

		tcr := decodeTCR(raw, enc, reader.format)

		var tc string
		if len(tcr.RawText) >= 2 {
			tc = tcr.RawText[0:2]
		}

		isDraftFamily := catalog.IsDraftDataFamily(tc)

		if !isDraftFamily {
			flush()
			txnNum++
			transactions = append(transactions, Transaction{
				TransactionNumber: txnNum,
				TransactionCode:   tc,
				TCRs:              []TCRRecord{tcr},
			})
			lastOrdinal = -1
			continue
		}

		_, ordinal, _ := tcrTypeForChar(byteAt(tcr.RawText, 3))

		startNew := current == nil || current.TransactionCode != tc || ordinal <= lastOrdinal
		if startNew {
			flush()
			txnNum++
			current = &Transaction{TransactionNumber: txnNum, TransactionCode: tc}
		}
		current.TCRs = append(current.TCRs, tcr)
		lastOrdinal = ordinal
	}
}
