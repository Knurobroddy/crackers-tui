# Crackers Modinst

Installs curated modpacks for your games in a few keystrokes. One file, no setup.

Supported: **Valheim** (Steam) on Windows, and on Linux via Proton.

## Download

Get the latest release from the [Releases page](https://github.com/Knurobroddy/crackers-tui/releases/latest):

- Windows: `crackers-modinst_<version>_windows_amd64.zip`
- Linux: `crackers-modinst_<version>_linux_amd64.tar.gz`

Unpack it anywhere.

## Use

1. Close the game.
2. Run `crackers-modinst.exe` (Windows, double-click) or `./crackers-modinst` (Linux, in a terminal).
3. Pick your game, then a modpack, and choose **Install**.
4. Start the game as usual.

From the same menu you can **update** a pack when a new version is available, or **remove** it to get the vanilla game back. The app updates itself when a new version is released.

Your own changes to mod settings are kept when a pack is updated.

## Troubleshooting

- **Game not detected on Linux:** Valheim must run through Proton (Steam → Properties → Compatibility) and must have been started once.
- **Permission denied on Windows:** run the app as administrator.
- **"Leftover mod files found":** mods installed by hand earlier are in the way. Let the app delete them, or remove them yourself.
- **Something else:** the log file `crackers-modinst.log` is in your temp folder (`%TEMP%` on Windows, `/tmp` on Linux). Attach it when you report a problem.
