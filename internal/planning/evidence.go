package planning

import (
	"encoding/json"
	"math/big"
	"regexp"
	"strings"
)

var quotedNumber = regexp.MustCompile(`\d+(?:\.\d+)?`)

// Numeric facts must occur in their cited excerpt. This checks provenance, not
// whether a user's request is feasible. Unit conversion should cite both values
// or keep the field unknown; the model can still discuss the candidate.
func numericEvidence(raw json.RawMessage, quote string) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	var supported func(any) bool
	supported = func(v any) bool {
		switch n := v.(type) {
		case float64:
			data, _ := json.Marshal(n)
			want, ok := new(big.Rat).SetString(string(data))
			if !ok {
				return false
			}
			for _, token := range quotedNumber.FindAllString(strings.ReplaceAll(quote, ",", ""), -1) {
				got, ok := new(big.Rat).SetString(token)
				if ok && got.Cmp(want) == 0 {
					return true
				}
			}
			return false
		case []any:
			for _, item := range n {
				if !supported(item) {
					return false
				}
			}
		}
		return true
	}
	return supported(value)
}
