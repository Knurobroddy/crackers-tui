package steam

import (
	"os"
	"strings"

	"github.com/andygrunwald/vdf"
)

// parseVDFFile parses a text VDF/ACF file. The parser unescapes "\\" to "\"
// (verified by the escaped-path fixture test), so no manual unescaping is done.
func parseVDFFile(file string) (map[string]any, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return vdf.NewParser(f).Parse()
}

// lookup returns the value of key in m, matching keys case-insensitively.
// An exact match wins over a case-insensitive one.
func lookup(m map[string]any, key string) (any, bool) {
	if v, ok := m[key]; ok {
		return v, true
	}
	for k, v := range m {
		if strings.EqualFold(k, key) {
			return v, true
		}
	}
	return nil, false
}

func lookupMap(m map[string]any, key string) (map[string]any, bool) {
	v, ok := lookup(m, key)
	if !ok {
		return nil, false
	}
	mm, ok := v.(map[string]any)
	return mm, ok
}

func lookupString(m map[string]any, key string) (string, bool) {
	v, ok := lookup(m, key)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}
