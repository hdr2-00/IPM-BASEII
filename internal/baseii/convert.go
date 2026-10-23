package baseii

import (
	"encoding/csv"
	"io"
	"sort"
)

// formulaTriggerChars mirrors internal/convert's CSV/formula-injection
// defense (OWASP CSV Injection) - same reasoning applies here: every value
// in this CSV originates from the file's own data.
const formulaTriggerChars = "=+-@\t\r"

func sanitizeCSVField(v string) string {
	if v == "" {
		return v
	}
	for _, c := range formulaTriggerChars {
		if rune(v[0]) == c {
			return "'" + v
		}
	}
	return v
}

// InScope reports whether a transaction should appear in the flattened CSV
// by default: the Draft Data family (TC 05 and its shared-layout siblings)
// only, excluding file/batch control records (TC 90/91/92) unless
// includeAll is set - the same "presentment/chargeback records only by
// default" convention used for the Mastercard IPM converter.
func InScope(t Transaction, includeAll bool) bool {
	if t.ParseErr != nil {
		return false
	}
	if includeAll {
		return true
	}
	_, isDraftFamily := lookupFunctionName(t.TransactionCode)
	if !isDraftFamily {
		return false
	}
	return catalogHasFamily(t.TransactionCode)
}

// BuildHeader computes the CSV column header: "TransactionCode", then
// every distinct field tag seen across all in-scope transactions, sorted
// for determinism.
func BuildHeader(txns []Transaction, includeAll bool) []string {
	seen := map[string]bool{}
	for _, t := range txns {
		if !InScope(t, includeAll) {
			continue
		}
		for _, tcr := range t.TCRs {
			for _, f := range tcr.Fields {
				seen[f.Tag] = true
			}
		}
	}
	tags := make([]string, 0, len(seen))
	for tag := range seen {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	header := make([]string, 0, len(tags)+1)
	header = append(header, "TransactionCode")
	header = append(header, tags...)
	return header
}

// WriteCSV writes a flattened CSV, one row per in-scope transaction,
// columns being the union of every TCR field tag across the file.
func WriteCSV(w io.Writer, txns []Transaction, includeAll bool) error {
	header := BuildHeader(txns, includeAll)
	cw := csv.NewWriter(w)
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, t := range txns {
		if !InScope(t, includeAll) {
			continue
		}
		values := map[string]string{"TransactionCode": t.TransactionCode}
		for _, tcr := range t.TCRs {
			for _, f := range tcr.Fields {
				values[f.Tag] = f.Value
			}
		}
		row := make([]string, len(header))
		for i, tag := range header {
			row[i] = sanitizeCSVField(values[tag])
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
