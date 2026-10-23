// Command ipmvalidate runs structural and business-rule validation against
// a Mastercard IPM clearing file and writes a summary report.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"mciipm/internal/convert"
	"mciipm/internal/iso8583"
	"mciipm/internal/validate"
)

func main() {
	var (
		inputPath   = flag.String("in", "", "path to the input IPM clearing file (required)")
		reportPath  = flag.String("report", "", "path to write the text report (default: stdout)")
		encoding    = flag.String("encoding", "auto", "record encoding: auto, ascii, or ebcdic")
		blocked     = flag.Bool("blocked", false, "input file uses IBM 1014-byte block framing on top of VBS")
		role        = flag.String("role", "org", "validate as 'org' (file you are sending) or 'dst' (file you received)")
		maxFindings = flag.Int("max-findings", 500, "maximum number of individual findings to list in the report (0 = unlimited)")
	)
	flag.Parse()

	if *inputPath == "" {
		fmt.Fprintln(os.Stderr, "usage: ipmvalidate -in <file> [-report <file.txt>] [-encoding auto|ascii|ebcdic] [-blocked] [-role org|dst] [-max-findings N]")
		os.Exit(2)
	}

	var r validate.Role
	switch *role {
	case "org":
		r = validate.RoleOrg
	case "dst":
		r = validate.RoleDst
	default:
		fmt.Fprintf(os.Stderr, "error: -role must be 'org' or 'dst', got %q\n", *role)
		os.Exit(2)
	}

	enc, err := resolveEncoding(*inputPath, *encoding, *blocked)
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

	opt := convert.Options{Encoding: enc, Blocked1014: *blocked}
	results, err := convert.ReadAll(in, opt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading input: %v\n", err)
		os.Exit(1)
	}

	summary := validate.Summarize(results, validate.Options{Role: r})

	out := os.Stdout
	if *reportPath != "" {
		f, err := os.Create(*reportPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		out = f
	}

	validate.WriteReport(out, summary, validate.Options{Role: r}, *maxFindings)

	fmt.Fprintf(os.Stderr, "validated %d records (encoding=%s): %d errors, %d warnings\n",
		summary.TotalRecords, enc, summary.TotalErrors, summary.TotalWarnings)

	if summary.TotalErrors > 0 {
		os.Exit(1)
	}
}

func resolveEncoding(path, requested string, blocked bool) (iso8583.Encoding, error) {
	switch requested {
	case "ascii":
		return iso8583.EncodingASCII, nil
	case "ebcdic":
		return iso8583.EncodingEBCDIC, nil
	case "auto":
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
