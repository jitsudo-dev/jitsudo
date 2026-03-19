// Package types contains exported types shared between the CLI, SDK, and server.
//
// License: Apache 2.0
package types

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration wraps time.Duration to support YAML string form (e.g. "8h", "30m").
// The default Go YAML encoding of time.Duration is nanoseconds, which is
// not operator-friendly. Use this type for any duration field in a config struct.
type Duration struct {
	time.Duration
}

// UnmarshalYAML parses a YAML string like "8h" or "30m" into a Duration.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	dur, err := time.ParseDuration(value.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", value.Value, err)
	}
	d.Duration = dur
	return nil
}

// MarshalYAML serialises back to a human-readable string.
func (d Duration) MarshalYAML() (interface{}, error) {
	return d.Duration.String(), nil
}
