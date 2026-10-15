package config

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var byteUnits = map[string]float64{
	"":    1,
	"b":   1,
	"kb":  1e3,
	"mb":  1e6,
	"gb":  1e9,
	"tb":  1e12,
	"k":   1024, // bare K/M/G/T and the *iB forms are binary
	"m":   1024 * 1024,
	"g":   1024 * 1024 * 1024,
	"t":   1024 * 1024 * 1024 * 1024,
	"kib": 1024,
	"mib": 1024 * 1024,
	"gib": 1024 * 1024 * 1024,
	"tib": 1024 * 1024 * 1024 * 1024,
}

// ParseByteSize parses a byte size such as "0", "500MB", "5GB", "500MiB" or
// "1.5 GB". Units are case-insensitive. KB/MB/GB/TB are decimal (powers of
// 1000); KiB/MiB/GiB/TiB and the bare K/M/G/T are binary (powers of 1024). A
// bare number is a count of bytes. Negative values and unknown units are
// errors.
func ParseByteSize(s string) (int64, error) {
	orig := s
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("invalid byte size %q: empty", orig)
	}
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
		i++
	}
	num, unit := s[:i], strings.ToLower(strings.TrimSpace(s[i:]))
	if num == "" {
		return 0, fmt.Errorf("invalid byte size %q: must start with a number (e.g. 500MB, 5GB, 0)", orig)
	}
	mult, ok := byteUnits[unit]
	if !ok {
		return 0, fmt.Errorf("invalid byte size %q: unknown unit %q (use B, KB, MB, GB, TB, KiB, MiB, GiB, TiB)", orig, s[i:])
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid byte size %q: bad number %q", orig, num)
	}
	v := f * mult
	if v >= math.MaxInt64 {
		return 0, fmt.Errorf("invalid byte size %q: too large", orig)
	}
	return int64(v), nil
}

// ByteSize is a size in bytes that unmarshals from YAML as either a number
// (bytes) or a string accepted by ParseByteSize.
type ByteSize int64

// UnmarshalYAML implements yaml.Unmarshaler.
func (b *ByteSize) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: byte size must be a scalar like 5GB or 0", n.Line)
	}
	v, err := ParseByteSize(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	*b = ByteSize(v)
	return nil
}
