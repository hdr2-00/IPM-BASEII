package main

import (
	"encoding/csv"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTruncatedInputReturnsFailure(t *testing.T) {
	if input := os.Getenv("VISA2CSV_TEST_INPUT"); input != "" {
		os.Args = []string{"visa2csv", "-in", input, "-out", os.Getenv("VISA2CSV_TEST_OUTPUT"), "-framing", "fixed", "-include-all"}
		main()
		os.Exit(0)
	}
	dir := t.TempDir()
	input, output := filepath.Join(dir, "input.ctf"), filepath.Join(dir, "output.csv")
	raw := make([]byte, 169)
	for i := range raw {
		raw[i] = '0'
	}
	copy(raw, "90")
	if err := os.WriteFile(input, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestTruncatedInputReturnsFailure$")
	cmd.Env = append(os.Environ(), "VISA2CSV_TEST_INPUT="+input, "VISA2CSV_TEST_OUTPUT="+output)
	log, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("exit = %v; want status 1; output: %s", err, log)
	}
	f, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil || len(rows) != 2 {
		t.Fatalf("partial CSV has %d rows, %v; want header and one record", len(rows), err)
	}
}
