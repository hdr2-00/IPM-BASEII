// Package money renders clearing-file amounts, which are transmitted as
// integers in a currency's minor units with the decimal point implied.
// Where that point sits depends on the currency: most have two decimal
// places, but a dozen have none and several Gulf-region currencies have
// three. Formatting every amount as if it had two silently misstates
// those by a factor of 100 or 1000.
package money

import (
	"strconv"
	"strings"
)

// DefaultExponent is the exponent assumed for any currency not listed in
// the exceptions below, which is correct for the large majority of
// ISO 4217 codes.
const DefaultExponent = 2

// exponents lists non-default currency exponents by numeric code.
// Settlement codes without an ISO minor unit use a display fallback.
var exponents = map[string]int{
	// Zero decimal places.
	"108": 0, // BIF Burundian franc
	"152": 0, // CLP Chilean peso
	"174": 0, // KMF Comorian franc
	"262": 0, // DJF Djiboutian franc
	"324": 0, // GNF Guinean franc
	"352": 0, // ISK Icelandic krona
	"392": 0, // JPY Japanese yen
	"410": 0, // KRW South Korean won
	"600": 0, // PYG Paraguayan guarani
	"646": 0, // RWF Rwandan franc
	"704": 0, // VND Vietnamese dong
	"800": 0, // UGX Ugandan shilling
	"950": 0, // XAF Central African CFA franc
	"952": 0, // XOF West African CFA franc
	"953": 0, // XPF CFP franc
	"960": 0, // XDR special drawing rights
	"962": 0, // (settlement-only code, no minor unit)

	// Three decimal places.
	"048": 3, // BHD Bahraini dinar
	"368": 3, // IQD Iraqi dinar
	"400": 3, // JOD Jordanian dinar
	"414": 3, // KWD Kuwaiti dinar
	"434": 3, // LYD Libyan dinar
	"512": 3, // OMR Omani rial
	"788": 3, // TND Tunisian dinar

	// Four decimal places.
	"927": 4, // UYW Uruguayan nominal wage index unit
	"990": 4, // CLF Chilean unidad de fomento
}

// Exponent returns the number of implied decimal places for an ISO 4217
// numeric currency code. Unknown or blank codes get DefaultExponent,
// since assuming the common case beats refusing to display a figure.
func Exponent(currencyCode string) int {
	code := strings.TrimSpace(currencyCode)
	for len(code) > 0 && len(code) < 3 { // "40" -> "040"
		code = "0" + code
	}
	if e, ok := exponents[code]; ok {
		return e
	}
	return DefaultExponent
}

// Format renders a minor-unit amount with the given number of implied
// decimal places, e.g. Format(123456, 3) == "123.456". Negative amounts
// keep their sign in front of the whole figure.
func Format(minorUnits int64, exponent int) string {
	if exponent <= 0 {
		return strconv.FormatInt(minorUnits, 10)
	}
	neg := minorUnits < 0
	if neg {
		minorUnits = -minorUnits
	}
	digits := strconv.FormatInt(minorUnits, 10)
	for len(digits) <= exponent {
		digits = "0" + digits
	}
	out := digits[:len(digits)-exponent] + "." + digits[len(digits)-exponent:]
	if neg {
		out = "-" + out
	}
	return out
}

// FormatCurrency renders a minor-unit amount using the exponent the
// currency code implies.
func FormatCurrency(minorUnits int64, currencyCode string) string {
	return Format(minorUnits, Exponent(currencyCode))
}

// ParseExponents decodes a Mastercard PDS 0148 (Currency Exponents)
// value into a currency-code -> exponent map. The field is a run of
// 4-character groups, each a 3-digit ISO currency code followed by a
// single exponent digit (e.g. "8402" means code 840, exponent 2), with
// up to 15 occurrences. A trailing fragment that is not a whole group,
// or a non-numeric exponent, is skipped rather than guessed at.
//
// This is authoritative where present: the manual requires originators
// to populate it for every amount/currency pair in the message, and the
// clearing system rejects a transaction whose exponents are wrong for
// its codes. It therefore takes precedence over the table above.
func ParseExponents(pds0148 string) map[string]int {
	out := make(map[string]int)
	for i := 0; i+4 <= len(pds0148); i += 4 {
		code := pds0148[i : i+3]
		exp, err := strconv.Atoi(pds0148[i+3 : i+4])
		if err != nil {
			continue
		}
		if _, err := strconv.Atoi(code); err != nil {
			continue
		}
		out[code] = exp
	}
	return out
}
