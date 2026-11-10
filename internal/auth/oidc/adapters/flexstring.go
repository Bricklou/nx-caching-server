package adapters

import (
	"encoding/json"
	"fmt"
)

// flexString decodes a JSON string or number, since forges are not consistent
// about the type of numeric ids.
type flexString string

func (s *flexString) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*s = flexString(text)
		return nil
	}

	var number json.Number
	if err := json.Unmarshal(data, &number); err != nil {
		return fmt.Errorf("expected a string or a number: %w", err)
	}
	*s = flexString(number.String())
	return nil
}
