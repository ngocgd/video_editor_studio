package ffmpeg

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// A filtergraph is built only from typed values: integers, finite
// floats, lower-case enum words, arithmetic expressions over a closed
// character set, and plain file names inside the step's temp directory.
// Nothing a user typed can reach -filter_complex, so no value can close a
// quote, start a new filter or name a second file.

// Value is one filter option value. Build it with Int, Float, Enum,
// Expr, Size, File or Dir; the zero Value is invalid.
type Value struct {
	s   string
	err error
}

var (
	enumPattern  = regexp.MustCompile(`^[a-z0-9_]+$`)
	exprPattern  = regexp.MustCompile(`^[0-9A-Za-z_.+\-*/(), <>=]+$`)
	filePattern  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)
	dirPattern   = regexp.MustCompile(`^/[A-Za-z0-9/._-]*$`)
	keyPattern   = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
	labelPattern = regexp.MustCompile(`^([a-z][a-z0-9_]*|[0-9]+:[va])$`)
)

// Int is an integer option value.
func Int(v int) Value { return Value{s: strconv.Itoa(v)} }

// Float is a finite float option value in plain decimal notation.
func Float(v float64) Value {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return Value{err: fmt.Errorf("%w: a filter value is not finite", ErrRefused)}
	}
	return Value{s: strconv.FormatFloat(v, 'f', -1, 64)}
}

// Enum is a lower-case word such as a pixel format or a transition name.
func Enum(s string) Value {
	if !enumPattern.MatchString(s) {
		return Value{err: fmt.Errorf("%w: filter word %q is not allowed", ErrRefused, s)}
	}
	return Value{s: s}
}

// Expr is an ffmpeg arithmetic expression. It is single-quoted in the
// graph, so its commas stay inside the option; the character set has no
// quote, colon, backslash, bracket or semicolon to break out with.
func Expr(s string) Value {
	if !exprPattern.MatchString(s) {
		return Value{err: fmt.Errorf("%w: filter expression has a forbidden character", ErrRefused)}
	}
	return Value{s: "'" + s + "'"}
}

// Size is a WxH frame size.
func Size(w, h int) Value {
	if w <= 0 || h <= 0 {
		return Value{err: fmt.Errorf("%w: frame size must be positive", ErrRefused)}
	}
	return Value{s: strconv.Itoa(w) + "x" + strconv.Itoa(h)}
}

// File is a plain file name, resolved against the step's temp directory
// (ffmpeg runs with it as its working directory).
func File(name string) Value {
	if !filePattern.MatchString(name) {
		return Value{err: fmt.Errorf("%w: filter file %q is not a plain file name", ErrRefused, name)}
	}
	return Value{s: name}
}

// Dir is a fixed absolute directory baked into the worker image, such as
// the subtitle fonts directory.
func Dir(path string) Value {
	if !dirPattern.MatchString(path) || strings.Contains(path, "..") {
		return Value{err: fmt.Errorf("%w: filter directory is not allowed", ErrRefused)}
	}
	return Value{s: path}
}

// Opt is one key=value filter option.
type Opt struct {
	Key string
	Val Value
}

// Filter is one filter with its options, in order.
type Filter struct {
	Name string
	Opts []Opt
}

// F builds a Filter with its options in order.
func F(name string, opts ...Opt) Filter { return Filter{Name: name, Opts: opts} }

// O builds an Opt.
func O(key string, v Value) Opt { return Opt{Key: key, Val: v} }

// allowedFilters is every filter a render or media step may use. CUDA
// filters are deliberately absent: filtergraphs run on the CPU and the
// GPU is used, at most, by the encoder.
var allowedFilters = map[string]bool{
	"scale": true, "crop": true, "zoompan": true, "format": true, "setsar": true, "fps": true,
	"xfade": true, "ass": true, "trim": true, "setpts": true, "split": true, "concat": true,
	"atrim": true, "apad": true, "asetpts": true, "aresample": true, "aformat": true,
	"loudnorm": true, "ebur128": true, "anull": true, "null": true,
}

// Chain is one filter chain: [in...]f1,f2,...[out...].
type Chain struct {
	In      []string
	Filters []Filter
	Out     []string
}

// Graph is a whole -filter_complex value.
type Graph struct {
	Chains []Chain
}

// String validates g and renders it. An invalid value, filter, key or
// label refuses the whole graph.
func (g Graph) String() (string, error) {
	if len(g.Chains) == 0 {
		return "", fmt.Errorf("%w: empty filtergraph", ErrRefused)
	}
	chains := make([]string, 0, len(g.Chains))
	for _, c := range g.Chains {
		s, err := c.render()
		if err != nil {
			return "", err
		}
		chains = append(chains, s)
	}
	return strings.Join(chains, ";"), nil
}

func (c Chain) render() (string, error) {
	if len(c.Filters) == 0 {
		return "", fmt.Errorf("%w: empty filter chain", ErrRefused)
	}
	var b strings.Builder
	if err := writeLabels(&b, c.In); err != nil {
		return "", err
	}
	for i, f := range c.Filters {
		if i > 0 {
			b.WriteByte(',')
		}
		if !allowedFilters[f.Name] {
			return "", fmt.Errorf("%w: filter %q is not allowed", ErrRefused, f.Name)
		}
		b.WriteString(f.Name)
		for j, o := range f.Opts {
			if !keyPattern.MatchString(o.Key) {
				return "", fmt.Errorf("%w: filter option %q is not allowed", ErrRefused, o.Key)
			}
			if o.Val.err != nil {
				return "", o.Val.err
			}
			if o.Val.s == "" {
				return "", fmt.Errorf("%w: filter option %q has no value", ErrRefused, o.Key)
			}
			if j == 0 {
				b.WriteByte('=')
			} else {
				b.WriteByte(':')
			}
			b.WriteString(o.Key)
			b.WriteByte('=')
			b.WriteString(o.Val.s)
		}
	}
	if err := writeLabels(&b, c.Out); err != nil {
		return "", err
	}
	return b.String(), nil
}

func writeLabels(b *strings.Builder, labels []string) error {
	for _, l := range labels {
		if !labelPattern.MatchString(l) {
			return fmt.Errorf("%w: filter label %q is not allowed", ErrRefused, l)
		}
		b.WriteByte('[')
		b.WriteString(l)
		b.WriteByte(']')
	}
	return nil
}
