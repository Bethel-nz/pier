package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var spanPattern = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*([a-z]+)$`)

var spanUnits = map[string]time.Duration{
	"s": time.Second, "sec": time.Second, "secs": time.Second, "second": time.Second, "seconds": time.Second,
	"m": time.Minute, "min": time.Minute, "mins": time.Minute, "minute": time.Minute, "minutes": time.Minute,
	"h": time.Hour, "hr": time.Hour, "hrs": time.Hour, "hour": time.Hour, "hours": time.Hour,
	"d": 24 * time.Hour, "day": 24 * time.Hour, "days": 24 * time.Hour,
	"w": 7 * 24 * time.Hour, "wk": 7 * 24 * time.Hour, "week": 7 * 24 * time.Hour, "weeks": 7 * 24 * time.Hour,
}

// ParseSpan reads a length of time as people write it in pier.yaml: Go's
// forms (90s, 2m, 1h30m) and single units with a word, such as 2min, 1hr,
// 24 hours, 7d, or 2 weeks.
func ParseSpan(text string) (time.Duration, error) {
	text = strings.ToLower(strings.TrimSpace(text))
	if d, err := time.ParseDuration(text); err == nil {
		return d, nil
	}
	match := spanPattern.FindStringSubmatch(text)
	if match == nil {
		return 0, fmt.Errorf("%q is not a length of time", text)
	}
	unit, ok := spanUnits[match[2]]
	if !ok {
		return 0, fmt.Errorf("%q has an unknown unit %q", text, match[2])
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0, err
	}
	return time.Duration(value * float64(unit)), nil
}
