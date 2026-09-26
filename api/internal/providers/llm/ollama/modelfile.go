package ollama

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ModelsMountDir is where the models volume is mounted in the worker,
// the only process that imports GGUF files into Ollama. Modelfiles in
// models/ollama name their weights by this absolute path.
const ModelsMountDir = "/models"

// Modelfile is the subset of Ollama's Modelfile syntax the offline
// import accepts: one FROM naming a local GGUF, PARAMETER lines, and
// optional SYSTEM and TEMPLATE text. Anything else (ADAPTER, MESSAGE,
// LICENSE, a FROM that names a registry model) is refused, so an import
// can only ever read the one pinned, verified weights file.
type Modelfile struct {
	From       string
	Parameters map[string]any
	System     string
	Template   string
}

// ParseModelfile parses text into a Modelfile. Values of SYSTEM and
// TEMPLATE may be single-line or wrapped in triple quotes across lines.
func ParseModelfile(text string) (Modelfile, error) {
	mf := Modelfile{Parameters: map[string]any{}}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		directive, rest, _ := strings.Cut(line, " ")
		rest = strings.TrimSpace(rest)
		switch strings.ToUpper(directive) {
		case "FROM":
			if mf.From != "" {
				return Modelfile{}, errors.New("more than one FROM")
			}
			if !strings.HasPrefix(rest, "/") || !strings.HasSuffix(strings.ToLower(rest), ".gguf") {
				return Modelfile{}, fmt.Errorf("FROM %q must be an absolute path to a .gguf file", rest)
			}
			mf.From = rest
		case "PARAMETER":
			key, value, ok := strings.Cut(rest, " ")
			if !ok || key == "" {
				return Modelfile{}, fmt.Errorf("PARAMETER %q needs a name and a value", rest)
			}
			addParameter(mf.Parameters, key, strings.TrimSpace(value))
		case "SYSTEM", "TEMPLATE":
			value, next, err := quotedValue(rest, lines, i)
			if err != nil {
				return Modelfile{}, fmt.Errorf("%s: %w", directive, err)
			}
			i = next
			if strings.EqualFold(directive, "SYSTEM") {
				mf.System = value
			} else {
				mf.Template = value
			}
		default:
			return Modelfile{}, fmt.Errorf("directive %q is not allowed in an offline import", directive)
		}
	}
	if mf.From == "" {
		return Modelfile{}, errors.New("no FROM")
	}
	return mf, nil
}

// addParameter stores a parameter value as the JSON type Ollama expects:
// numbers as numbers, "stop" (repeatable) as a list, anything else as a
// string.
func addParameter(params map[string]any, key, value string) {
	value = strings.Trim(value, `"`)
	if key == "stop" {
		list, _ := params[key].([]string)
		params[key] = append(list, value)
		return
	}
	if n, err := strconv.ParseInt(value, 10, 64); err == nil {
		params[key] = n
		return
	}
	if f, err := strconv.ParseFloat(value, 64); err == nil {
		params[key] = f
		return
	}
	params[key] = value
}

// quotedValue reads a directive value starting on lines[i]: either the
// rest of the line, or a """-delimited block that may span lines. It
// returns the value and the index of the last line consumed.
func quotedValue(rest string, lines []string, i int) (string, int, error) {
	const quote = `"""`
	if !strings.HasPrefix(rest, quote) {
		return strings.Trim(rest, `"`), i, nil
	}
	body := strings.TrimPrefix(rest, quote)
	if end := strings.Index(body, quote); end >= 0 {
		return body[:end], i, nil
	}
	parts := []string{body}
	for j := i + 1; j < len(lines); j++ {
		if end := strings.Index(lines[j], quote); end >= 0 {
			parts = append(parts, lines[j][:end])
			return strings.Join(parts, "\n"), j, nil
		}
		parts = append(parts, lines[j])
	}
	return "", i, errors.New(`unterminated """ block`)
}
