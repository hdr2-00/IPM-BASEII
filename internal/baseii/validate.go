package baseii

import (
	"fmt"
	"strconv"
	"strings"

	"mciipm/internal/baseii/catalog"
)

// Severity distinguishes hard failures from informational findings.
type Severity string

const (
	SeverityError   Severity = "ERROR"
	SeverityWarning Severity = "WARNING"
)

// Finding is a single validation error or warning against one transaction.
type Finding struct {
	TransactionNumber int
	Severity          Severity
	Rule              string
	Tag               string
	Message           string
}

func (f Finding) String() string {
	tag := f.Tag
	if tag != "" {
		tag = " [" + tag + "]"
	}
	return fmt.Sprintf("transaction %d: %s %s%s: %s", f.TransactionNumber, f.Severity, f.Rule, tag, f.Message)
}

// ValidateTransaction checks draft records and numeric control fields.
// Unknown transaction codes have no field-level validation.
func ValidateTransaction(t Transaction) []Finding {
	var findings []Finding

	if t.ParseErr != nil {
		return []Finding{{
			TransactionNumber: t.TransactionNumber,
			Severity:          SeverityError,
			Rule:              "structural.parse",
			Message:           t.ParseErr.Error(),
		}}
	}

	if !catalogHasFamily(t.TransactionCode) {
		for _, tcr := range t.TCRs {
			findings = append(findings, businessRuleChecks(t.TransactionNumber, tcr)...)
		}
		return findings
	}

	for _, tcr := range t.TCRs {
		if len(tcr.RawText) != ctfRecordLength {
			findings = append(findings, Finding{
				TransactionNumber: t.TransactionNumber,
				Severity:          SeverityError,
				Rule:              "structural.record_length",
				Message:           fmt.Sprintf("draft record has %d bytes; expected %d", len(tcr.RawText), ctfRecordLength),
			})
			continue
		}
		if tcr.TCRType == "" {
			findings = append(findings, Finding{
				TransactionNumber: t.TransactionNumber,
				Severity:          SeverityError,
				Rule:              "structural.unrecognised_tcr",
				Message:           fmt.Sprintf("position 4 (Transaction Component Sequence Number) contains %q, not a recognised TCR indicator (0-7, D, E)", tcr.SequenceNumber),
			})
			continue
		}
		if !tcr.Classified {
			findings = append(findings, Finding{
				TransactionNumber: t.TransactionNumber,
				Severity:          SeverityWarning,
				Rule:              "structural.unclassified_variant",
				Tag:               tcr.TCRType,
				Message:           variantWarningMessage(tcr),
			})
		}
		findings = append(findings, businessRuleChecks(t.TransactionNumber, tcr)...)
	}

	return findings
}

func catalogHasFamily(tc string) bool {
	return catalog.IsDraftDataFamily(tc)
}

func variantWarningMessage(tcr TCRRecord) string {
	if tcr.TCRType == "TCR2" {
		return "TCR2 is country-specific (Argentina/Brazil/Chile/Colombia/Japan/Mexico/Russia/South Korea/Sweden/Turkey) and the manual gives no in-record discriminator; field-level detail was not resolved, captured as raw text only"
	}
	return fmt.Sprintf("no documented variant's discriminator value matched this %s record; field-level detail may be unreliable", tcr.TCRType)
}

// businessRuleChecks applies format-based and semantic checks: UN
// (unpacked numeric) fields must contain only digits, Account Number
// passes a modulus-10 (Luhn) check per the manual's own edit criteria,
// and date-like fields are calendrically valid.
func businessRuleChecks(txnNum int, tcr TCRRecord) []Finding {
	var findings []Finding

	for _, f := range tcr.Fields {
		if f.Format == "UN" && !isAllDigitsOrBlank(f.Value) {
			findings = append(findings, Finding{
				TransactionNumber: txnNum,
				Severity:          SeverityError,
				Rule:              "business.non_numeric_field",
				Tag:               f.Tag,
				Message:           fmt.Sprintf("%s must be unpacked numeric but got %q", f.Name, f.Value),
			})
		}
	}

	if tcr.TCRType == "TCR0" {
		if acct, ok := fieldValue(tcr, "TCR0.AccountNumber"); ok && strings.TrimSpace(acct) != "" {
			if !luhnValid(strings.TrimLeft(acct, "0")) && !luhnValid(acct) {
				findings = append(findings, Finding{
					TransactionNumber: txnNum,
					Severity:          SeverityWarning,
					Rule:              "business.modulus10_check",
					Tag:               "TCR0.AccountNumber",
					Message:           "Account Number failed the modulus-10 check digit algorithm",
				})
			}
		}
		if pd, ok := fieldValue(tcr, "TCR0.PurchaseDateMMDD"); ok && len(pd) == 4 {
			if err := validateMMDD(pd); err != nil {
				findings = append(findings, Finding{
					TransactionNumber: txnNum,
					Severity:          SeverityError,
					Rule:              "business.invalid_date",
					Tag:               "TCR0.PurchaseDateMMDD",
					Message:           err.Error(),
				})
			}
		}
		if cpd, ok := fieldValue(tcr, "TCR0.CentralProcessingDateYDDD"); ok && len(cpd) == 4 {
			if err := validateYDDD(cpd); err != nil {
				findings = append(findings, Finding{
					TransactionNumber: txnNum,
					Severity:          SeverityError,
					Rule:              "business.invalid_date",
					Tag:               "TCR0.CentralProcessingDateYDDD",
					Message:           err.Error(),
				})
			}
		}
	}

	return findings
}

func fieldValue(tcr TCRRecord, tag string) (string, bool) {
	for _, f := range tcr.Fields {
		if f.Tag == tag {
			return f.Value, true
		}
	}
	return "", false
}

func isAllDigitsOrBlank(s string) bool {
	if strings.TrimSpace(s) == "" {
		return true // UN fields are often legitimately zero/blank-filled when not applicable
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func luhnValid(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	sum := 0
	alt := false
	for i := len(s) - 1; i >= 0; i-- {
		d := int(s[i] - '0')
		if alt {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		alt = !alt
	}
	return sum%10 == 0
}

func validateMMDD(mmdd string) error {
	mm, err1 := strconv.Atoi(mmdd[0:2])
	dd, err2 := strconv.Atoi(mmdd[2:4])
	if err1 != nil || err2 != nil {
		return fmt.Errorf("non-numeric MMDD value %q", mmdd)
	}
	if mm < 1 || mm > 12 {
		return fmt.Errorf("month %02d is not 01-12", mm)
	}
	maxDay := 31
	switch mm {
	case 4, 6, 9, 11:
		maxDay = 30
	case 2:
		maxDay = 29 // permissive on leap years; only MMDD is available, no year
	}
	if dd < 1 || dd > maxDay {
		return fmt.Errorf("day %02d is not valid for month %02d", dd, mm)
	}
	return nil
}

func validateYDDD(yddd string) error {
	ddd, err := strconv.Atoi(yddd[1:4])
	if err != nil {
		return fmt.Errorf("non-numeric YDDD value %q", yddd)
	}
	if ddd < 1 || ddd > 366 {
		return fmt.Errorf("Julian day %03d is not 001-366", ddd)
	}
	return nil
}
