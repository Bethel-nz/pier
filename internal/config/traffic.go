package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Throttle is how Pier slows a service, as written: a preset such as "3g",
// or explicit latency and bandwidth.
type Throttle struct {
	Preset  string
	Latency string `yaml:"latency"`
	Down    string `yaml:"down"`
	Up      string `yaml:"up"`
}

// UnmarshalYAML accepts `throttle: 3g` as well as the mapping form.
func (t *Throttle) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		t.Preset = strings.TrimSpace(node.Value)
		return nil
	}
	// node.Decode drops the loader's strictness, so check keys here.
	for i := 0; i+1 < len(node.Content); i += 2 {
		if key := node.Content[i].Value; key != "latency" && key != "down" && key != "up" {
			return fmt.Errorf("line %d: throttle has no field %q; use latency, down, and up", node.Content[i].Line, key)
		}
	}
	type plain Throttle
	return node.Decode((*plain)(t))
}

// Shaping is a resolved throttle. The zero value means full speed.
type Shaping struct {
	Latency time.Duration
	// Down and Up are bytes per second toward the client and toward the
	// service; 0 leaves that direction unlimited.
	Down int64
	Up   int64
}

// IsZero reports whether the shaping changes nothing.
func (s Shaping) IsZero() bool { return s == Shaping{} }

// throttlePresets follow the browser devtools profiles people already know.
var throttlePresets = map[string]Shaping{
	"slow-3g": {Latency: 2000 * time.Millisecond, Down: 400_000 / 8, Up: 400_000 / 8},
	"3g":      {Latency: 560 * time.Millisecond, Down: 1_600_000 / 8, Up: 750_000 / 8},
	"4g":      {Latency: 170 * time.Millisecond, Down: 9_000_000 / 8, Up: 1_500_000 / 8},
}

// maxLatency keeps a typo like 3000s from hanging every request.
const maxLatency = 60 * time.Second

func resolveThrottle(t *Throttle) (Shaping, error) {
	if t == nil {
		return Shaping{}, nil
	}
	if t.Preset != "" {
		shaping, ok := throttlePresets[strings.ToLower(t.Preset)]
		if !ok {
			return Shaping{}, fmt.Errorf("%q is not a preset; use slow-3g, 3g, or 4g, or set latency, down, and up", t.Preset)
		}
		return shaping, nil
	}
	var shaping Shaping
	if t.Latency != "" {
		latency, err := time.ParseDuration(t.Latency)
		if err != nil || latency < 0 || latency > maxLatency {
			return Shaping{}, fmt.Errorf("latency %q must be a duration up to 60s, such as 300ms", t.Latency)
		}
		shaping.Latency = latency
	}
	for _, rate := range []struct {
		text string
		out  *int64
		name string
	}{{t.Down, &shaping.Down, "down"}, {t.Up, &shaping.Up, "up"}} {
		if rate.text == "" {
			continue
		}
		bytes, err := parseRate(rate.text)
		if err != nil {
			return Shaping{}, fmt.Errorf("%s %q must be a rate such as 750kbit or 1.5mbit", rate.name, rate.text)
		}
		*rate.out = bytes
	}
	if shaping.IsZero() {
		return Shaping{}, fmt.Errorf("needs a preset or at least one of latency, down, and up")
	}
	return shaping, nil
}

var ratePattern = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*(k|m|g)(?:bit|bps|bit/s)$`)

// parseRate turns "750kbit" or "1.5mbps" into bytes per second.
func parseRate(text string) (int64, error) {
	match := ratePattern.FindStringSubmatch(strings.ToLower(strings.TrimSpace(text)))
	if match == nil {
		return 0, fmt.Errorf("invalid rate")
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid rate")
	}
	bits := map[string]float64{"k": 1e3, "m": 1e6, "g": 1e9}[match[2]] * value
	return max(int64(bits/8), 1), nil
}

// Capture limits keep a forgotten capture from filling the disk.
const (
	minCapture = time.Minute
	maxCapture = 30 * 24 * time.Hour
)

func resolveCapture(text string) (time.Duration, error) {
	if text == "" {
		return 0, nil
	}
	keep, err := ParseSpan(text)
	if err != nil || keep < minCapture || keep > maxCapture {
		return 0, fmt.Errorf("%q must be how long to keep requests, from 1min to 30d, such as 2min, 1hr, 24h, or 7d", text)
	}
	return keep, nil
}
