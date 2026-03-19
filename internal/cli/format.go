package cli

import "strings"

// stateString trims the verbose proto enum prefix from a RequestState value.
func stateString(s interface{ String() string }) string {
	return strings.TrimPrefix(s.String(), "REQUEST_STATE_")
}
