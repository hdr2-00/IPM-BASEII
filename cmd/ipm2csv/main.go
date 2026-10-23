// Command ipm2csv converts a Mastercard IPM clearing file (presentments,
// addenda, and chargebacks) into a single flattened CSV file, with raw
// DE/PDS field numbers as column headers.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"mciipm/internal/convert"
	"mciipm/internal/iso8583"
)

func main() {
	var (
		inputPath  = flag.String("in", "", "path to the input IPM clearing file (required)")
		outputPath = flag.String("out", "", "path to write the output CSV (required)")
		encoding   = flag.String("encoding", "auto", "record encoding: auto, ascii, or ebcdic")
		blocked    = flag.Bool("blocked", false, "input file uses IBM 1014-byte block framing on top of VBS")
		includeAll = flag.Bool("include-all", false, "include file header/trailer and other non-presentment/chargeback records too")
	)
	flag.Parse()

	if *inputPath == "" || *outputPath == "" {
		fmt.Fprintln(os.Stderr, "usage: ipm2csv -in <file> -out <file.csv> [-encoding auto|ascii|ebcdic] [-blocked] [-include-all]")
		os.Exit(2)
	}

	in, err := os.Open(*inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer in.Close()

	enc, err := resolveEncoding(*inputPath, *encoding, *blocked)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	opt := convert.Options{Encoding: enc, Blocked1014: *blocked, IncludeAll: *includeAll}

	results, err := convert.ReadAll(in, opt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading input: %v\n", err)
		os.Exit(1)
	}

	failed := 0
	inScope := 0
	for _, rr := range results {
		if rr.ParseErr != nil {
			failed++
			fmt.Fprintf(os.Stderr, "warning: record %d failed to parse and will be skipped: %v\n", rr.RecordNumber, rr.ParseErr)
			continue
		}
		if convert.InScope(rr, opt) {
			inScope++
		}
	}

	out, err := os.Create(*outputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer out.Close()

	if err := convert.WriteCSV(out, results, opt); err != nil {
		fmt.Fprintf(os.Stderr, "error writing CSV: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "done: %d records read, %d written to CSV, %d failed to parse (encoding=%s)\n",
		len(results), inScope, failed, enc)
}

// resolveEncoding handles "auto" by peeking at the first record's MTI bytes.
func resolveEncoding(path, requested string, blocked bool) (iso8583.Encoding, error) {
	switch requested {
	case "ascii":
		return iso8583.EncodingASCII, nil
	case "ebcdic":
		return iso8583.EncodingEBCDIC, nil
	case "auto":
		// fall through to sniffing below
	default:
		return "", fmt.Errorf("unknown -encoding value %q (want auto, ascii, or ebcdic)", requested)
	}

	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	results, err := convert.ReadAll(f, convert.Options{Encoding: iso8583.EncodingASCII, Blocked1014: blocked, IncludeAll: true, MaxRecords: 1, KeepRaw: true})
	if err != nil && !errors.Is(err, convert.ErrTooManyRecords) || len(results) == 0 || len(results[0].Raw) < 4 {
		return iso8583.EncodingASCII, nil
	}
	return iso8583.DetectEncoding(results[0].Raw[:4]), nil
}
