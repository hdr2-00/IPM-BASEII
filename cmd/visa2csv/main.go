// Command visa2csv converts a Visa BASE II Center Transaction File (CTF)
// or Interchange Transaction File (ITF) - TC 05 Draft Data family - into
// a single flattened CSV, with field names as column headers.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"mciipm/internal/baseii"
)

func main() {
	var (
		inputPath  = flag.String("in", "", "path to the input CTF/ITF file (required)")
		outputPath = flag.String("out", "", "path to write the output CSV (required)")
		encoding   = flag.String("encoding", "auto", "record encoding: auto, ascii, or ebcdic")
		format     = flag.String("format", "ctf", "record shape: ctf (168 bytes) or itf (170 bytes)")
		framingOpt = flag.String("framing", "auto", "record framing: auto, fixed (no delimiter, per the manual), or lines (CRLF/LF-terminated, common in real-world exports)")
		includeAll = flag.Bool("include-all", false, "include File Header/Batch Trailer/File Trailer control records too")
	)
	flag.Parse()

	if *inputPath == "" || *outputPath == "" {
		fmt.Fprintln(os.Stderr, "usage: visa2csv -in <file> -out <file.csv> [-encoding auto|ascii|ebcdic] [-format ctf|itf] [-framing auto|fixed|lines]")
		os.Exit(2)
	}

	fileFormat, err := parseFormat(*format)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}

	framing, err := resolveFraming(*inputPath, *framingOpt, fileFormat)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	enc, err := resolveEncoding(*inputPath, *encoding, fileFormat, framing)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	in, err := os.Open(*inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer in.Close()

	reader := baseii.NewReader(in, fileFormat, framing)
	txns, err := baseii.GroupTransactions(reader, enc, 0)
	scanAborted := errors.Is(err, baseii.ErrScanAborted)
	switch {
	case scanAborted:
		// Keep what was read, but never let a truncated scan pass for a
		// complete one: the output below covers a prefix of the file.
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
		fmt.Fprintf(os.Stderr, "warning: output covers only the %d transaction(s) read before that point\n", len(txns))
	case err != nil:
		fmt.Fprintf(os.Stderr, "error reading input: %v\n", err)
		os.Exit(1)
	}

	failed := 0
	written := 0
	for _, t := range txns {
		if t.ParseErr != nil {
			failed++
			fmt.Fprintf(os.Stderr, "warning: transaction %d failed to parse and will be skipped: %v\n", t.TransactionNumber, t.ParseErr)
		}
		if baseii.InScope(t, *includeAll) {
			written++
		}
	}

	out, err := os.Create(*outputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer out.Close()

	if err := baseii.WriteCSV(out, txns, *includeAll); err != nil {
		fmt.Fprintf(os.Stderr, "error writing CSV: %v\n", err)
		os.Exit(1)
	}
	if err := out.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "error closing CSV: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "done: %d transactions read, %d written to CSV, %d failed to parse (encoding=%s, format=%s, framing=%s)\n",
		len(txns), written, failed, enc, *format, framing)
	if scanAborted {
		os.Exit(1)
	}
}

func parseFormat(s string) (baseii.Format, error) {
	switch s {
	case "ctf":
		return baseii.FormatCTF, nil
	case "itf":
		return baseii.FormatITF, nil
	default:
		return "", fmt.Errorf("-format must be 'ctf' or 'itf', got %q", s)
	}
}

func resolveFraming(path, requested string, format baseii.Format) (baseii.Framing, error) {
	switch requested {
	case "fixed":
		return baseii.FramingFixed, nil
	case "lines":
		return baseii.FramingLines, nil
	case "auto":
	default:
		return "", fmt.Errorf("-framing must be auto, fixed, or lines, got %q", requested)
	}

	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	sample := make([]byte, format.RecordLength()+8)
	n, _ := f.Read(sample)
	return baseii.DetectFraming(sample[:n], format), nil
}

func resolveEncoding(path, requested string, format baseii.Format, framing baseii.Framing) (baseii.Encoding, error) {
	switch requested {
	case "ascii":
		return baseii.EncodingASCII, nil
	case "ebcdic":
		return baseii.EncodingEBCDIC, nil
	case "auto":
	default:
		return "", fmt.Errorf("unknown -encoding value %q (want auto, ascii, or ebcdic)", requested)
	}

	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	reader := baseii.NewReader(f, format, framing)
	raw, err := reader.Next()
	if err != nil || len(raw) < 2 {
		return baseii.EncodingASCII, nil
	}
	return baseii.DetectEncoding(raw), nil
}
