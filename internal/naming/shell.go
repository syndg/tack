package naming

import "strings"

// ShellQuote wraps a value in single quotes with proper escaping for safe
// shell interpolation. Each embedded single quote is replaced with the
// standard POSIX idiom: end quote, escaped quote, start quote.
//
// Example: ShellQuote("foo'bar") → "'foo'\\''bar'"
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
