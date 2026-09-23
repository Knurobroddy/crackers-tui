package packer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// valheimTemplate is the pack.modinst written by Init: BepInEx from
// Thunderstore (referenced, not re-hosted), the Proton DLL override,
// BepInEx/ as the owned folder so runtime configs and logs are removed too,
// and BepInEx/config preserved so updates keep the user's mod settings.
const valheimTemplate = `{
  "id": "%s",
  "game_id": "valheim",
  "name": "%s",
  "description": "One line shown in the pack list.",
  "version": "%s",
  "owned_dirs": ["BepInEx"],
  "preserve": ["BepInEx/config"],
  "external": {
    "windows": [
      {
        "url": "https://thunderstore.io/package/download/denikson/BepInExPack_Valheim/5.4.2202/",
        "kind": "zip",
        "dest": "",
        "zip_root": "BepInExPack_Valheim/"
      }
    ]
  },
  "hooks": [
    { "type": "proton_dll_override", "builds": ["linux_proton"], "dll": "winhttp", "mode": "native,builtin" }
  ]
}
`

// Init creates a new Valheim pack folder with a pack.modinst template and an
// empty common/BepInEx/plugins/ folder for mod DLLs.
func Init(packDir string) (string, error) {
	metaPath := filepath.Join(packDir, MetaFileName)
	if _, err := os.Stat(metaPath); err == nil {
		return "", fmt.Errorf("%s already exists", metaPath)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	abs, err := filepath.Abs(packDir)
	if err != nil {
		return "", err
	}
	id := slug(filepath.Base(abs))
	if id == "" {
		id = "my-pack"
	}
	if err := os.MkdirAll(filepath.Join(packDir, "common", "BepInEx", "plugins"), 0o755); err != nil {
		return "", err
	}
	content := fmt.Sprintf(valheimTemplate, id, title(id), time.Now().Format("2006.01.02"))
	if err := os.WriteFile(metaPath, []byte(content), 0o644); err != nil {
		return "", err
	}
	return metaPath, nil
}

// slug turns a folder name into a pack id.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-._")
}

func title(id string) string {
	words := strings.FieldsFunc(id, func(r rune) bool { return r == '-' || r == '_' || r == '.' })
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}
