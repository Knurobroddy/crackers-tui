package hooks

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// Pure editor for Wine registry text files (user.reg). Only the lines that
// change are touched; everything else, including line endings, stays
// byte-identical.
//
// section is given as it appears between the brackets in the file, i.e. with
// doubled backslashes: `Software\\Wine\\DllOverrides`.

type regLine struct {
	text string // without line ending
	eol  string // "\n", "\r\n" or "" (last line without newline)
}

func splitLines(data []byte) []regLine {
	var lines []regLine
	s := string(data)
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			lines = append(lines, regLine{text: s})
			break
		}
		text, eol := s[:i], "\n"
		if strings.HasSuffix(text, "\r") {
			text, eol = text[:len(text)-1], "\r\n"
		}
		lines = append(lines, regLine{text: text, eol: eol})
		s = s[i+1:]
	}
	return lines
}

func joinLines(lines []regLine) []byte {
	var b bytes.Buffer
	for _, l := range lines {
		b.WriteString(l.text)
		b.WriteString(l.eol)
	}
	return b.Bytes()
}

// detectEOL returns the file's line ending: CRLF if any line uses it, else LF.
func detectEOL(data []byte) string {
	if bytes.Contains(data, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}

// sectionName returns the key name of a section header line.
func sectionName(text string) (string, bool) {
	if !strings.HasPrefix(text, "[") {
		return "", false
	}
	end := strings.LastIndexByte(text, ']')
	if end < 1 {
		return "", false
	}
	return text[1:end], true
}

// findSection returns the header index of section (case-insensitive) and the
// index one past its last line.
func findSection(lines []regLine, section string) (start, end int) {
	start = -1
	for i, l := range lines {
		name, ok := sectionName(l.text)
		if !ok {
			continue
		}
		if start >= 0 {
			return start, i
		}
		if strings.EqualFold(name, section) {
			start = i
		}
	}
	return start, len(lines)
}

// parseValueLine splits `"name"=rest` into the raw (escaped) name and rest.
func parseValueLine(text string) (name, rest string, ok bool) {
	if !strings.HasPrefix(text, `"`) {
		return "", "", false
	}
	for i := 1; i < len(text); i++ {
		switch text[i] {
		case '\\':
			i++ // skip escaped character
		case '"':
			if i+1 < len(text) && text[i+1] == '=' {
				return text[1:i], text[i+2:], true
			}
			return "", "", false
		}
	}
	return "", "", false
}

func findValue(lines []regLine, start, end int, name string) int {
	want := regEscape(name)
	for i := start + 1; i < end; i++ {
		if n, _, ok := parseValueLine(lines[i].text); ok && strings.EqualFold(n, want) {
			return i
		}
	}
	return -1
}

// insertIndex is where a new value goes: after the header and any "#..."
// metadata lines such as "#time=".
func insertIndex(lines []regLine, start, end int) int {
	i := start + 1
	for i < end && strings.HasPrefix(lines[i].text, "#") {
		i++
	}
	return i
}

func regEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

func valueLine(rawName, rawValue string) string {
	return `"` + rawName + `"=` + rawValue
}

func insertLine(lines []regLine, at int, l regLine, eol string) []regLine {
	if at > 0 && lines[at-1].eol == "" {
		lines[at-1].eol = eol // previous line was the unterminated last line
	}
	lines = append(lines, regLine{})
	copy(lines[at+1:], lines[at:])
	lines[at] = l
	return lines
}

func appendSection(data []byte, section, line string, now int64) []byte {
	eol := detectEOL(data)
	out := append([]byte(nil), data...)
	out = append(out, eol+"["+section+"] "+strconv.FormatInt(now, 10)+eol+line+eol...)
	return out
}

// SetRegValue sets `"name"="value"` in section. It returns the new content and
// the previous raw value (the text after "=", e.g. `"builtin"`), or nil if the
// value did not exist. A missing section is appended with timestamp now.
func SetRegValue(data []byte, section, name, value string, now int64) ([]byte, *string, error) {
	if name == "" {
		return nil, nil, fmt.Errorf("empty registry value name")
	}
	newValue := `"` + regEscape(value) + `"`
	lines := splitLines(data)
	start, end := findSection(lines, section)
	if start < 0 {
		return appendSection(data, section, valueLine(regEscape(name), newValue), now), nil, nil
	}
	if i := findValue(lines, start, end, name); i >= 0 {
		rawName, prev, _ := parseValueLine(lines[i].text)
		lines[i].text = valueLine(rawName, newValue)
		return joinLines(lines), &prev, nil
	}
	eol := detectEOL(data)
	lines = insertLine(lines, insertIndex(lines, start, end), regLine{text: valueLine(regEscape(name), newValue), eol: eol}, eol)
	return joinLines(lines), nil, nil
}

// RestoreRegValue undoes SetRegValue: with previous == nil the value line is
// deleted, otherwise its raw value is restored. The section header is left in
// place.
func RestoreRegValue(data []byte, section, name string, previous *string) ([]byte, error) {
	if name == "" {
		return nil, fmt.Errorf("empty registry value name")
	}
	lines := splitLines(data)
	start, end := findSection(lines, section)
	if start < 0 {
		if previous == nil {
			return data, nil
		}
		return appendSection(data, section, valueLine(regEscape(name), *previous), 0), nil
	}
	i := findValue(lines, start, end, name)
	switch {
	case i < 0 && previous == nil:
		return data, nil
	case i < 0:
		eol := detectEOL(data)
		lines = insertLine(lines, insertIndex(lines, start, end), regLine{text: valueLine(regEscape(name), *previous), eol: eol}, eol)
	case previous == nil:
		lines = append(lines[:i], lines[i+1:]...)
	default:
		rawName, _, _ := parseValueLine(lines[i].text)
		lines[i].text = valueLine(rawName, *previous)
	}
	return joinLines(lines), nil
}
