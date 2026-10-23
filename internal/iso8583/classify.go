package iso8583

import "mciipm/internal/catalog"

// ClassifyResult describes what a parsed message was identified as.
type ClassifyResult struct {
	Key           string // canonical catalog.Messages key, "" if unrecognised
	FunctionName  string // human-readable transaction function name
	Category      string // Presentment / Chargeback / Addendum / File Control / ...
	AddendumType  string // resolved addendum subtype name, only set for 1644:696
	Recognised    bool   // false if MTI/FunctionCode combo isn't in the manual's table at all
	Disambiguated bool   // false if it's a 1644:696 message whose Usage Code/IRN pair didn't resolve to a specific subtype
}

// Classify determines the canonical message-type key for a parsed message,
// resolving Financial Detail Addendum sub-types via PDS 0501 (Usage Code +
// Industry Record Number) where possible.
func Classify(msg *Message) ClassifyResult {
	fc := msg.FunctionCode()
	tf, recognised := catalog.LookupFunction(msg.MTI, fc)

	res := ClassifyResult{Recognised: recognised}
	if recognised {
		res.FunctionName = tf.Name
		res.Category = tf.Category
	}

	baseKey := msg.MTI + ":" + fc

	if baseKey != "1644:696" {
		res.Key = baseKey
		return res
	}

	// Financial Detail Addendum: disambiguate via PDS 0501 subfields.
	// PDS 0501 is a fixed 16-char field: [2 UsageCode][3 IndustryRecordNumber][3 Occurrence][8 AssocFirstPresentmentNum]
	pds0501, ok := msg.Get("PDS0501")
	if !ok || len(pds0501) < 5 {
		res.Key = baseKey
		return res
	}
	usageCode := pds0501[0:2]
	irn := pds0501[2:5]
	if name, ok := catalog.UsageCodeAddendum[[2]string{usageCode, irn}]; ok {
		res.Key = "1644:696:" + usageCode + ":" + irn
		res.AddendumType = name
		res.Disambiguated = true
		return res
	}

	res.Key = baseKey
	return res
}
