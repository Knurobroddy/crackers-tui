# Crackers Modinst

A single static binary for Windows and Linux (amd64) with a terminal UI. It detects supported games, then installs or removes one curated modpack per game from a public remote library. It also updates itself from GitHub Releases.

- MVP game: **Valheim** via Steam. On Linux only the Proton build is supported; a native Linux install is not detected (the `not_found_hint` explains why).
- Design records: [`.claude/ADR-001.md`](.claude/ADR-001.md) is the original decision and MVP scope; [`.claude/ADR-002.md`](.claude/ADR-002.md) documents the system as built (all additions and deviations) and wins where they differ.
- Remote library: <https://github.com/Knurobroddy/modinst-packs> (served from `https://raw.githubusercontent.com/Knurobroddy/modinst-packs/main/`).

## Build

Requires Go (version per `go.mod`). No cgo, no runtime dependencies.

```sh
go vet ./...
go test ./...

# Static release-style builds (version injected via ldflags; without it the version is "dev")
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=0.1.0" -o dist/crackers-modinst.exe ./cmd/crackers-modinst
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=0.1.0" -o dist/crackers-modinst     ./cmd/crackers-modinst
```

Some tests only run on Linux (`internal/detect/steam/steam_linux_test.go`, real `$HOME`-based Steam discovery) and the file-mode assertions in the engine tests. CI runs the suite on both `ubuntu-latest` and `windows-latest`.

## Release

1. Tag and push: `git tag v0.1.0 && git push origin v0.1.0`.
2. The `release` job in `.github/workflows/ci.yml` runs GoReleaser (`.goreleaser.yaml`), which publishes:
   - `crackers-modinst_<version>_windows_amd64.zip` containing `crackers-modinst.exe`
   - `crackers-modinst_<version>_linux_amd64.tar.gz` containing `crackers-modinst`
   - `checksums.txt` (self-update validates the archive against it)

Every push to `main` and every pull request runs `go vet`, `go test` and a `goreleaser release --snapshot` build (tags run the real release instead). Self-update picks the newest non-draft, non-prerelease GitHub release whose tag is semver and which is newer than the running version.

## Repositories (ADR §14)

| What | Where | Value |
|---|---|---|
| Remote library base URL | `internal/config/config.go` → `RemoteBaseURL` | `https://raw.githubusercontent.com/Knurobroddy/modinst-packs/main/` ([repo](https://github.com/Knurobroddy/modinst-packs)) |
| GitHub repo for self-update | `internal/config/config.go` → `GitHubRepo` | `Knurobroddy/crackers-tui` |
| Go module path | `go.mod` and every import | `github.com/Knurobroddy/crackers-tui` |

Renaming either repository (or the library's branch) cuts released apps off from their library or updates. Self-update is disabled for `dev` builds.

## Running

Double-click the `.exe` on Windows, or run the binary in a terminal. The log file is `crackers-modinst.log` in the OS temp directory (`%TEMP%` / `/tmp`) and is truncated at each start.

Hidden flags (not shown in the UI):

| Flag | Effect |
|---|---|
| `--remote <url>` | Use another remote library base URL. |
| `--no-update` | Skip the self-update check. |
| `--detect-only` | Print the detection results as JSON to stdout and exit. |
| `--version` | Print the version and exit. |

The remote URL is taken from `--remote`, else from the `CRACKERS_MODINST_REMOTE` environment variable, else from `RemoteBaseURL`.

## Remote library

Static files over HTTPS, fetched fresh on every start. Full schemas and semantics: ADR §3 ([games.json](.claude/ADR-001.md#31-gamesjson-detection-rules), [index.json](.claude/ADR-001.md#32-indexjson-pack-catalog), [pack manifest](.claude/ADR-001.md#33-pack-manifest-own-format)).

```
<base>/games.json            detection rules (data only)
<base>/index.json            pack catalog
<base>/packs/<pack-id>.json  pack manifests
<base>/files/...             mod files and archives (convention; manifests hold full or relative URLs)
```

- Every document has `schema_version`; `games.json` and `index.json` also have `min_app_version`. A newer schema or an unmet minimum version forces the update prompt.
- Relative URLs are resolved against `<base>`. Unknown JSON fields are ignored.
- Every file entry has `sha256` and `size` of the downloaded object. Everything is downloaded and verified before anything in the game directory is touched.

[`testdata/remote/`](testdata/remote) is a complete example library (a fake BepInEx pack plus one fake mod) with correct hashes; `internal/remote/testdata_test.go` keeps it consistent.

### Hosting

The base URL must serve files **by path** (`<base>/games.json`, `<base>/index.json`, `<base>/packs/…`). Any static host works:

- **Recommended for testing and small libraries:** a public GitHub repo, base `https://raw.githubusercontent.com/<owner>/<library-repo>/main/`. Git refuses files over 100 MB; put big files in that repo's GitHub Releases and reference them by absolute URL. raw.githubusercontent.com caches for about 5 minutes, which is harmless because `modinst-pack` gives changed files new names.
- Other path-based hosts: GitHub Pages, Cloudflare R2 or Backblaze B2 public buckets, Netlify, any web server.
- **Google Drive cannot be the base URL**: it has no path-based URLs (only per-file IDs), so `games.json`/`index.json` cannot be found. It can host individual files referenced by absolute URL (`https://drive.usercontent.google.com/download?id=<ID>&export=download`), with caveats: files over ~100 MB get an HTML "can't scan for viruses" page instead of the file, and popular files hit download quotas. Installs fail safely (checksum mismatch) in both cases.
- Mods from Thunderstore don't need re-hosting: reference the versioned download URL (`https://thunderstore.io/package/download/<namespace>/<name>/<version>/`). The sha256 in the manifest pins the exact bytes.

## Creating a modpack

`modinst-pack` (authoring tool, not shipped to players) turns a pack folder into a manifest, zips and an `index.json` entry:

```sh
go build -o dist/modinst-pack ./cmd/modinst-pack

dist/modinst-pack init  packs-src/friends-pack           # pack.modinst template + common/BepInEx/plugins/
# put mod files under packs-src/friends-pack/common/ exactly as they go into the game folder
dist/modinst-pack build packs-src/friends-pack library/   # library/ = the folder you upload
```

Pack folder:

```
friends-pack/
  pack.modinst     metadata (JSON)
  common/          files for every build, mirroring the game root (e.g. common/BepInEx/plugins/Mod.dll)
  windows/         files only for builds whose "files" list is "windows" (Valheim on Windows and Proton)
  linux/           files only for builds whose "files" list is "linux"
```

`pack.modinst` (the `init` template for Valheim):

```json
{
  "id": "friends-pack",
  "game_id": "valheim",
  "name": "Friends Pack",
  "description": "One line shown in the pack list.",
  "version": "2026.09.22",
  "owned_dirs": ["BepInEx"],
  "preserve": ["BepInEx/config"],
  "external": {
    "windows": [
      { "url": "https://thunderstore.io/package/download/denikson/BepInExPack_Valheim/5.4.2202/",
        "kind": "zip", "dest": "", "zip_root": "BepInExPack_Valheim/" }
    ]
  },
  "hooks": [
    { "type": "proton_dll_override", "builds": ["linux_proton"], "dll": "winhttp", "mode": "native,builtin" }
  ]
}
```

- `external` entries are downloaded only to compute `sha256` and `size` (give `sha256` yourself to pin an expected value). Thunderstore mod zips hold the DLL either at the zip root or under `plugins/`; use e.g. `"kind": "zip", "dest": "BepInEx/plugins/<ModName>"` with `"zip_root": ""` or `"plugins/"` to match, or unpack the DLL into `common/` instead.
- `build` validates the result with the same rules the installer uses (path safety, zip entries, no file written twice) for every build combination, and checks `game_id` against `library/games.json`.
- Packs cannot overwrite files: shipping your own `BepInEx/config/BepInEx.cfg` next to the Thunderstore BepInExPack (which contains one) is rejected as "written twice". Put per-mod configs (`BepInEx/config/<mod>.cfg`) in `common/`.
- Output: `library/packs/<id>.json`, `library/files/<id>-<list>-<hash>.zip` (content-addressed, so unchanged files keep their name) and the pack's entry in `library/index.json` (created if missing; other entries are kept). Rebuilding unchanged input produces a byte-identical manifest, so players only see "Update available" after a real change.
- To publish an update, bump `version`, rebuild and upload the library folder. Keep old `files/` for a while; clients that loaded the previous manifest still need them.
- `games.json` is maintained by hand (see `testdata/remote/games.json`).

## Local smoke test

Serve the example library:

```sh
python -m http.server 8765 --bind 127.0.0.1 --directory testdata/remote
```

On **Linux** (or WSL), build a fake Steam install under a temporary `HOME`:

```sh
H=/tmp/cm-home; S=$H/.local/share/Steam
mkdir -p $S/steamapps/common/Valheim $S/steamapps/compatdata/892970/pfx
printf '"libraryfolders"\n{\n\t"0"\n\t{\n\t\t"path"\t\t"%s"\n\t}\n}\n' "$S" > $S/steamapps/libraryfolders.vdf
printf '"AppState"\n{\n\t"appid"\t\t"892970"\n\t"installdir"\t\t"Valheim"\n}\n' > $S/steamapps/appmanifest_892970.acf
echo vanilla > $S/steamapps/common/Valheim/valheim.exe          # the anchor (Proton build)
cp testdata/wine/user.reg $S/steamapps/compatdata/892970/pfx/user.reg

HOME=$H ./dist/crackers-modinst --remote http://127.0.0.1:8765/ --no-update
```

Then check:

1. The main menu shows `Valheim — Not installed`.
2. Install: the files and `.crackers-modinst.json` appear in the game folder, and `user.reg` gains `"winhttp"="native,builtin"`.
3. The status shows `Installed`.
4. Edit `testdata/remote/packs/valheim-test.json` (e.g. the `version`; revert it afterwards), choose **Detect again**: `Update available`.
5. Remove: the game folder and `user.reg` are back to their exact pre-install state (a `user.reg.modinst-bak` backup stays next to it).
6. Rename `valheim.exe` and choose **Detect again**: the "No supported games were detected" screen with the Proton hint.

On **Windows**, detection reads the Steam path from the registry, so it only finds real Steam installs.

## Project layout

```
cmd/crackers-modinst/     flags, version, wiring
cmd/modinst-pack/         pack authoring CLI (init, build)
internal/config/          names, repository URLs, supported schema versions
internal/packer/          pack.modinst, deterministic zips, manifest + index.json output
internal/remote/          HTTP client, JSON types, URL resolution, download + verify
internal/detect/          Strategy interface, registry, GameDef types, build selection
internal/detect/steam/    Steam roots (registry / $HOME), libraryfolders.vdf, appmanifest ACF
internal/engine/          plan, install, rollback, remove, marker, status
internal/engine/pathsafe/ path safety (ADR §5.2)
internal/hooks/           Hook + undo registries, proton_dll_override, pure user.reg editor
internal/tui/             Bubble Tea screens
internal/update/          self-update via go-selfupdate
testdata/                 VDF/ACF fixtures, user.reg fixture, example remote
```

## Implementation notes

Choices made where the ADR left room, kept to the simplest option in MVP scope:

- **Install order.** ADR §5.3 lists "remove the existing pack" before "download", while §5.2 requires aborting "before writing anything" when a zip entry is unsafe, which can only be checked after downloading. The engine therefore does: pre-flight → download and verify every file → expand the plan (validating every zip entry) → move the existing pack (if any) aside into `<root>/.crackers-modinst-old/` → check that no target exists → write → keep preserved user files → undo the old pack's hooks → new hooks → marker → delete the staged pack. A failed download, an unsafe archive, a declined leftovers prompt or a failed write never loses the currently installed pack: it is moved back. Only a failure while applying hooks or writing the marker loses the old pack's hook changes (the error says to reinstall). An install interrupted by a crash is recovered by the next install or remove.
- **Preserved files** (addition): the optional manifest field `preserve` (e.g. `["BepInEx/config"]`) keeps the user's files on reinstall/update of the same pack. The marker records each shipped file's SHA-256 (`file_sha256`); a preserved file the user changed wins over the pack's new version, an unchanged one is updated, and one the pack does not ship (e.g. written by a mod) is kept. Remove and switching packs still delete everything.
- **Path safety is stricter than listed and OS-independent:** `\` is treated as a path separator (so `..\x` in a zip is caught on Linux too), drive letters and UNC prefixes are rejected on every OS, and `:` (NTFS streams) and NUL bytes are rejected. `owned_dirs` and file targets may not be the game root itself. Symlink entries anywhere in an archive reject the pack. Existing path components that are not real directories (symlinks, junctions, files) fail the install.
- **Zip handling:** entries outside `zip_root` are ignored; a non-empty `zip_root` that matches nothing is an error; directory entries are created and recorded in `dirs_created`. On Linux, file modes are kept when the archive carries Unix permissions (otherwise 0644, or 0755 if marked executable). On Windows files are always written 0644, since a missing write bit would create read-only files.
- **Pack sanity checks:** the manifest's `id`/`game_id` must match the index entry; unknown file `kind`s and unknown hook types reject the pack (asking to update the app); the same target twice, a path used both as a file and a directory, or a file named `.crackers-modinst.json` are pack errors.
- **VDF:** `andygrunwald/vdf` already unescapes `\\` to `\` (covered by `testdata/steam/libraryfolders_new.vdf`, including a UNC path), so no manual unescaping is done. Keys are matched case-insensitively. In `libraryfolders.vdf` only numeric keys are treated as libraries (skips `TimeNextStatsReport`, `ContentStatsID` in the old format). `installdir` must be a single path segment.
- **Detection data:** besides `steam_library` and `steam_appid`, results carry `steam_libraries` (all libraries, joined with the OS path-list separator) so the Proton hook can look for the prefix in other libraries. Roots and libraries are deduplicated after resolving symlinks (case-insensitively on Windows).
- **`user.reg` editing:** `previous` in the marker stores the raw text after `=` (e.g. `"builtin"` including quotes), so restoring is byte-exact. New values go after the section header and any following `#...` metadata lines (a superset of `#time=`). Line endings are detected (CRLF if present, else LF) and preserved. The `user.reg.modinst-bak` backup is kept. If `user.reg` no longer exists at undo time (the prefix was deleted), the undo is a logged no-op, so removal is not blocked forever.
- **Remove:** marker paths are validated with the same path-safety rules before deleting anything. An unreadable marker shows status "Unknown" and Remove reports the error without deleting the marker.
- **Status:** "Update available" compares the SHA-256 of the raw remote manifest with the marker. If the manifest cannot be fetched, the status is "Installed (could not check for updates)".
- **Self-update:** the archive entry is found by the canonical binary name rather than the running file's name, so a renamed download (`crackers-modinst (1).exe`) still updates in place; the release is validated against `checksums.txt` before replacing the executable. The hidden `.<exe>.old` file that Windows cannot delete while the old process runs is removed on the next start. `dev` builds skip both the update check and `min_app_version`. The update check runs concurrently with loading the library; the menu waits at most for its 5 s timeout.
- **Remote URL:** must be `http` or `https` (`file://` is rejected); plain `http` is allowed for local testing. JSON documents are capped at 32 MiB.
- **Retries:** network errors, HTTP 429 and 5xx are retried up to 4 attempts with 1 s / 2 s / 4 s backoff (or `Retry-After`, capped at 30 s). Thunderstore was seen answering a burst of 13 package downloads with a 500. Other 4xx fail immediately.
- **TUI:** quitting is disabled for the whole install/remove operation (downloads included), not only while writing files. "Detect again" also refetches the library. Returning from a result screen re-runs detection so the statuses are current.
- **Layout:** the UI fills the terminal: banner and a panel (at most 96 columns) centered, key help on the last row. The banner is the largest that fits: the one-line art (needs 127+ columns), CRACKERS stacked over MODINST (fits the default 120×30 Windows console), or the plain title "Crackers Modinst vX.Y.Z".
- **Leftovers** (extends ADR §5.3 step 4): with no marker present, existing files the pack would write *and* existing `owned_dirs` count as leftovers from an earlier manual mod install. An old `BepInEx/plugins` would otherwise keep loading stale mods next to the pack. The install stops with the list; the UI offers "delete them and install" (`InstallRequest.CleanLeftovers`). The game menu also has **Remove leftover mod files** (only when no pack is installed), which downloads the chosen pack to learn its paths and deletes just those. Files the pack does not know about are never touched, and there is no game-specific list of mod files in the code.
- **Go version:** `go.mod` requires Go 1.26 because the current `golang.org/x/sys` needs it (the ADR's floor is 1.22).
- **Naming:** temp and backup suffixes are `.modinst-tmp` / `.modinst-bak` (not a bare `.modinst`: the `user.reg` temp file would overwrite its backup). New file formats use the `.modinst` extension, e.g. `pack.modinst`.
- **Pack tool (ADR §13)** lives in this repo as `cmd/modinst-pack` so it reuses the manifest types, path safety and the installer's plan validation (`engine.ValidateFiles`). It is not part of the GoReleaser release: a second archive per OS would make self-update's asset matching ambiguous. Only the split layout (`common/`, `windows/`, `linux/`) is read; other top-level entries in the pack folder are reported and skipped, so notes like a README never end up in the game folder. Each list folder becomes one zip (fewer uploads than one entry per file) with sorted entries, a fixed timestamp and modes 0644/0755. Symlinks, `Thumbs.db`, `desktop.ini` and `.DS_Store` are not packed. `external` downloads go through the installer's HTTP client (same retries) and are rejected if they return an HTML page (login or confirmation pages).
