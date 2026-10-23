// Package validate implements structural and business-rule validation for
// parsed Mastercard IPM presentment/addendum/chargeback records, plus
// summary report generation.
package validate

import (
	"fmt"
	"strconv"
	"strings"

	"mciipm/internal/catalog"
	"mciipm/internal/convert"
)

// Severity distinguishes hard failures from informational findings.
type Severity string

const (
	SeverityError   Severity = "ERROR"
	SeverityWarning Severity = "WARNING"
)

// Role selects which requirement column (Org or Dst) of the manual's
// per-message field tables is used for mandatory-field checks. Use RoleOrg
// when validating a file you are sending (as an originator/acquirer); use
// RoleDst when validating a file you received (as a destination/issuer).
type Role string

const (
	RoleOrg Role = "org"
	RoleDst Role = "dst"
)

// containerDETags are the DEs that carry PDS TLV sequences and are therefore
// never present as a raw value in Message.Values (see Message.HasBit).
var containerDETags = map[string]bool{
	"DE48": true, "DE62": true, "DE123": true, "DE124": true, "DE125": true,
}

// Finding is a single validation error or warning against one record.
type Finding struct {
	RecordNumber int
	Severity     Severity
	Rule         string
	Tag          string
	Message      string
}

func (f Finding) String() string {
	tag := f.Tag
	if tag != "" {
		tag = " [" + tag + "]"
	}
	return fmt.Sprintf("record %d: %s %s%s: %s", f.RecordNumber, f.Severity, f.Rule, tag, f.Message)
}

// Options controls validation behaviour.
type Options struct {
	Role Role
}

// ValidateRecord runs all structural and business rule checks against one
// parsed record and returns any findings (empty slice if clean).
func ValidateRecord(rr convert.RecordResult, opt Options) []Finding {
	var findings []Finding

	if rr.ParseErr != nil {
		findings = append(findings, Finding{
			RecordNumber: rr.RecordNumber,
			Severity:     SeverityError,
			Rule:         "structural.parse",
			Message:      rr.ParseErr.Error(),
		})
		return findings
	}

	msg := rr.Msg
	cls := rr.Classify

	if !msg.SecondaryBit {
		findings = append(findings, Finding{
			RecordNumber: rr.RecordNumber,
			Severity:     SeverityError,
			Rule:         "structural.secondary_bitmap_flag",
			Tag:          "bit1",
			Message:      "bit 1 of the primary bitmap (secondary bitmap present) is not set, but the manual requires every IPM message to carry both bitmaps",
		})
	}

	for _, w := range msg.Warnings {
		findings = append(findings, Finding{
			RecordNumber: rr.RecordNumber,
			Severity:     SeverityWarning,
			Rule:         "structural.duplicate_pds",
			Message:      w,
		})
	}

	if !cls.Recognised {
		findings = append(findings, Finding{
			RecordNumber: rr.RecordNumber,
			Severity:     SeverityError,
			Rule:         "structural.unknown_function_code",
			Tag:          "DE24",
			Message:      fmt.Sprintf("MTI %s with Function Code %s is not a combination defined in the manual", msg.MTI, msg.FunctionCode()),
		})
		return findings
	}

	if cls.Key == "1644:696" && !cls.Disambiguated {
		findings = append(findings, Finding{
			RecordNumber: rr.RecordNumber,
			Severity:     SeverityWarning,
			Rule:         "structural.unresolved_addendum_type",
			Tag:          "PDS0501",
			Message:      "could not resolve Financial Detail Addendum sub-type from PDS 0501 (Usage Code/Industry Record Number); mandatory-field checks skipped for this record",
		})
	}

	// Mandatory field presence, per the manual's Org/Sys/Dst tables.
	if def, ok := messageDef(cls.Key, opt.Role); ok {
		for _, fr := range def.Fields {
			var reqCode string
			switch opt.Role {
			case RoleDst:
				reqCode = fr.Dst
			default:
				reqCode = fr.Org
			}
			if reqCode != "M" {
				continue
			}
			if fr.Tag == "DE1" {
				// DE1 (Bit Map, Secondary) is not a data value in our model -
				// the parser always reads the full 16-byte primary+secondary
				// bitmap per record, satisfying this requirement structurally.
				continue
			}
			if containerDETags[fr.Tag] {
				// PDS container fields (DE48/62/123/124/125) are expanded
				// into their constituent PDSxxxx values rather than kept as
				// a raw DEnn value, so check bitmap presence instead.
				deNum, _ := strconv.Atoi(strings.TrimPrefix(fr.Tag, "DE"))
				if !msg.HasBit(deNum) {
					findings = append(findings, Finding{
						RecordNumber: rr.RecordNumber,
						Severity:     SeverityError,
						Rule:         "structural.missing_mandatory_field",
						Tag:          fr.Tag,
						Message:      fmt.Sprintf("%s (%s) is mandatory for %s but is not present", fr.Tag, fr.Name, def.Title),
					})
				}
				continue
			}
			if _, present := msg.Get(fr.Tag); !present {
				findings = append(findings, Finding{
					RecordNumber: rr.RecordNumber,
					Severity:     SeverityError,
					Rule:         "structural.missing_mandatory_field",
					Tag:          fr.Tag,
					Message:      fmt.Sprintf("%s (%s) is mandatory for %s but is not present", fr.Tag, fr.Name, def.Title),
				})
			}
		}
	}

	findings = append(findings, businessRuleChecks(rr)...)

	return findings
}

// messageDef resolves a classification key to its catalog layout. File
// Header and File Trailer messages are catalogued twice - once
// customer-generated (":ORG") and once clearing-system-generated
// (":DST") - because the two have different field requirements, and
// Classify cannot tell them apart without knowing which side of the
// exchange is being validated. Role supplies that: a file you are sending
// carries your own customer-generated header and trailer, a file you
// received from the clearing system carries its system-generated ones.
func messageDef(key string, role Role) (catalog.MessageDef, bool) {
	if def, ok := catalog.Messages[key]; ok {
		return def, true
	}
	suffix := ":ORG"
	if role == RoleDst {
		suffix = ":DST"
	}
	def, ok := catalog.Messages[key+suffix]
	return def, ok
}

// businessRuleChecks applies representation-based and semantic checks:
// numeric fields must contain only digits, PAN should pass a Luhn check,
// and date-like fields must be calendrically valid.
func businessRuleChecks(rr convert.RecordResult) []Finding {
	var findings []Finding
	msg := rr.Msg

	for _, f := range msg.Fields {
		if f.Tag == "PDS0301" || f.Tag == "PDS0306" {
			if _, err := strconv.ParseInt(f.Value, 10, 64); err != nil || !isAllDigits(f.Value) {
				findings = append(findings, Finding{
					RecordNumber: rr.RecordNumber,
					Severity:     SeverityError,
					Rule:         "business.non_numeric_field",
					Tag:          f.Tag,
					Message:      fmt.Sprintf("%s must be an unsigned numeric control total", f.Tag),
				})
			}
		}
		if !strings.HasPrefix(f.Tag, "DE") {
			continue
		}
		numStr := strings.TrimPrefix(f.Tag, "DE")
		deNum, err := strconv.Atoi(numStr)
		if err != nil {
			continue
		}
		def, ok := catalog.Fields[numStr]
		if !ok {
			continue
		}
		if def.Representation == "n" && !isAllDigits(f.Value) {
			findings = append(findings, Finding{
				RecordNumber: rr.RecordNumber,
				Severity:     SeverityError,
				Rule:         "business.non_numeric_field",
				Tag:          f.Tag,
				Message:      fmt.Sprintf("%s (%s) must be all-numeric but got %q", f.Tag, def.Name, f.Value),
			})
		}

		switch deNum {
		case 2: // PAN - Luhn check (warning only: some BIN ranges/test PANs are not Luhn-valid)
			if !luhnValid(f.Value) {
				findings = append(findings, Finding{
					RecordNumber: rr.RecordNumber,
					Severity:     SeverityWarning,
					Rule:         "business.luhn_check",
					Tag:          f.Tag,
					Message:      "PAN failed Luhn checksum validation",
				})
			}
		case 14: // Expiration date YYMM
			if len(f.Value) == 4 {
				mm, _ := strconv.Atoi(f.Value[2:4])
				if mm < 1 || mm > 12 {
					findings = append(findings, Finding{
						RecordNumber: rr.RecordNumber,
						Severity:     SeverityError,
						Rule:         "business.invalid_date",
						Tag:          f.Tag,
						Message:      fmt.Sprintf("expiration month %q is not 01-12", f.Value[2:4]),
					})
				}
			}
		case 12: // Date/time local transaction YYMMDDhhmmss
			if len(f.Value) == 12 {
				if err := validateDateTime(f.Value); err != nil {
					findings = append(findings, Finding{
						RecordNumber: rr.RecordNumber,
						Severity:     SeverityError,
						Rule:         "business.invalid_date",
						Tag:          f.Tag,
						Message:      err.Error(),
					})
				}
			}
		case 73: // Date, Action YYMMDD
			if len(f.Value) == 6 {
				if err := validateDate(f.Value); err != nil {
					findings = append(findings, Finding{
						RecordNumber: rr.RecordNumber,
						Severity:     SeverityError,
						Rule:         "business.invalid_date",
						Tag:          f.Tag,
						Message:      err.Error(),
					})
				}
			}
		case 49, 50, 51: // currency codes - numeric 3-digit, non-zero
			if f.Value == "000" {
				findings = append(findings, Finding{
					RecordNumber: rr.RecordNumber,
					Severity:     SeverityWarning,
					Rule:         "business.suspicious_currency_code",
					Tag:          f.Tag,
					Message:      "currency code is 000",
				})
			}
		}
	}
	return findings
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func luhnValid(pan string) bool {
	if !isAllDigits(pan) || len(pan) < 8 {
		return false
	}
	sum := 0
	alt := false
	for i := len(pan) - 1; i >= 0; i-- {
		d := int(pan[i] - '0')
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

func validateDate(yymmdd string) error {
	mm, _ := strconv.Atoi(yymmdd[2:4])
	dd, _ := strconv.Atoi(yymmdd[4:6])
	return checkMonthDay(mm, dd)
}

func validateDateTime(yymmddhhmmss string) error {
	mm, _ := strconv.Atoi(yymmddhhmmss[2:4])
	dd, _ := strconv.Atoi(yymmddhhmmss[4:6])
	hh, _ := strconv.Atoi(yymmddhhmmss[6:8])
	mi, _ := strconv.Atoi(yymmddhhmmss[8:10])
	ss, _ := strconv.Atoi(yymmddhhmmss[10:12])
	if err := checkMonthDay(mm, dd); err != nil {
		return err
	}
	if hh > 23 || mi > 59 || ss > 59 {
		return fmt.Errorf("invalid time %02d:%02d:%02d", hh, mi, ss)
	}
	return nil
}

func checkMonthDay(mm, dd int) error {
	if mm < 1 || mm > 12 {
		return fmt.Errorf("month %02d is not 01-12", mm)
	}
	maxDay := 31
	switch mm {
	case 4, 6, 9, 11:
		maxDay = 30
	case 2:
		maxDay = 29 // permissive on leap years since only 2-digit year is available
	}
	if dd < 1 || dd > maxDay {
		return fmt.Errorf("day %02d is not valid for month %02d", dd, mm)
	}
	return nil
}
