// SPDX-License-Identifier: MIT

package reqid

// UnmarshalText sets c to the COR in text by ParseCorrelation's rule, so encoding/json, flag.TextVar and log/slog
// read a Correlation; empty text sets the zero Correlation, as MarshalText writes it; other text that is not a COR
// leaves c unchanged and returns an error.
func (c *Correlation) UnmarshalText(text []byte) error {
	if len(text) == 0 {
		*c = Correlation{}
		return nil
	}
	parsed, ok := ParseCorrelation(string(text))
	if !ok {
		return correlationTextError{}
	}
	*c = parsed
	return nil
}

// correlationTextError is the error of UnmarshalText for text that is not a COR id.
type correlationTextError struct{}

func (correlationTextError) Error() string {
	return "reqid: the text is not a COR id"
}
