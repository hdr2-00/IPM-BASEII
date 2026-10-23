package baseii

import "mciipm/internal/baseii/catalog"

// lookupFunctionName resolves a Transaction Code to its documented
// function name/category (Draft Data family, or a recognised control
// record).
func lookupFunctionName(code string) (catalog.TransactionFunction, bool) {
	return catalog.LookupFunction(code)
}

// LookupFunctionName is the exported form, returning just the function
// name, for callers outside this package (e.g. the web dashboard) that
// don't need the full TransactionFunction struct.
func LookupFunctionName(code string) (string, bool) {
	f, ok := catalog.LookupFunction(code)
	if !ok {
		return "", false
	}
	return f.Name, true
}
