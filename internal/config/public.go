package config

import (
	"fmt"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Public is a service's `public:` value: true, false, or how long to stay
// public after each pier up, such as 2h.
type Public struct {
	On bool
	// For is how long the service stays public after pier up; 0 is until pier down.
	For time.Duration
	raw string
}

// PublicFlag is `public: true` or `public: false`.
func PublicFlag(on bool) *Public { return &Public{On: on} }

// Limits for an expiring public link: long enough to share, short enough
// that a forgotten one closes.
const (
	minPublicFor = time.Minute
	maxPublicFor = 7 * 24 * time.Hour
)

// UnmarshalYAML accepts a boolean or a duration.
func (p *Public) UnmarshalYAML(node *yaml.Node) error {
	var on bool
	if err := node.Decode(&on); err == nil {
		*p = Public{On: on}
		return nil
	}
	*p = Public{On: true, raw: node.Value}
	if d, err := ParseSpan(node.Value); err == nil {
		p.For = d
	}
	return nil // Validate explains a bad value
}

// MarshalYAML writes the value back as it reads best.
func (p Public) MarshalYAML() (any, error) {
	if p.For > 0 {
		return p.For.String(), nil
	}
	return p.On, nil
}

func (p *Public) problem() string {
	if p == nil || p.raw == "" && p.For == 0 {
		return ""
	}
	if p.For < minPublicFor || p.For > maxPublicFor {
		return fmt.Sprintf("%s must be true, false, or a length of time from 1min to 7d, such as 30min, 2hr, or 1d", strconv.Quote(p.raw))
	}
	return ""
}
