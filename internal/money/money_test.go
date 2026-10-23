package money

import "testing"

func TestExponent(t *testing.T) {
	cases := map[string]int{
		"840": 2, // USD
		"978": 2, // EUR
		"132": 2, // CVE
		"928": 2, // VES
		"392": 0, // JPY - no minor unit
		"410": 0, // KRW
		"368": 3, // IQD - three decimal places
		"414": 3, // KWD
		"990": 4, // CLF
		"48":  3, // zero-padded short form of BHD
		"":    2, // unknown, assume the common case
		"999": 2,
	}
	for code, want := range cases {
		if got := Exponent(code); got != want {
			t.Errorf("Exponent(%q) = %d, want %d", code, got, want)
		}
	}
}

func TestFormat(t *testing.T) {
	cases := []struct {
		minor    int64
		exponent int
		want     string
	}{
		{123456, 2, "1234.56"},
		{123456, 0, "123456"}, // JPY: the integer is the amount
		{123456, 3, "123.456"},
		{5, 2, "0.05"},
		{5, 3, "0.005"},
		{0, 2, "0.00"},
		{-2550, 2, "-25.50"},
		{100, 4, "0.0100"},
	}
	for _, c := range cases {
		if got := Format(c.minor, c.exponent); got != c.want {
			t.Errorf("Format(%d, %d) = %q, want %q", c.minor, c.exponent, got, c.want)
		}
	}
}

// A JPY amount formatted at two decimal places understates it 100-fold,
// and an IQD amount overstates it 10-fold. This is the whole reason the
// exponent is carried around rather than assumed.
func TestFormatCurrencyAcrossExponents(t *testing.T) {
	if got := FormatCurrency(500000, "392"); got != "500000" {
		t.Errorf("JPY 500000 minor units = %q, want 500000", got)
	}
	if got := FormatCurrency(500000, "368"); got != "500.000" {
		t.Errorf("IQD 500000 minor units = %q, want 500.000", got)
	}
	if got := FormatCurrency(500000, "840"); got != "5000.00" {
		t.Errorf("USD 500000 minor units = %q, want 5000.00", got)
	}
}

func TestParseExponents(t *testing.T) {
	// Two occurrences: code 840 exponent 2, code 368 exponent 3.
	got := ParseExponents("8402" + "3683")
	if len(got) != 2 || got["840"] != 2 || got["368"] != 3 {
		t.Fatalf("ParseExponents = %v, want {840:2, 368:3}", got)
	}

	// A trailing fragment that is not a whole 4-character group is
	// ignored rather than guessed at.
	got = ParseExponents("8402" + "36")
	if len(got) != 1 || got["840"] != 2 {
		t.Fatalf("ParseExponents with trailing fragment = %v, want {840:2}", got)
	}

	if got := ParseExponents(""); len(got) != 0 {
		t.Fatalf("ParseExponents(\"\") = %v, want empty", got)
	}
}
