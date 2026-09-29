# Crackers Modinst

Installs curated modpacks for your games in a few keystrokes. One file, no setup.

Supported: **Valheim** (Steam) on Windows, and on Linux via Proton; the **Valheim Dedicated Server** on Windows and Linux.

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

## Dedicated servers

1. Stop the server.
2. Run the app on the machine the server runs on, as the user that owns the server folder.
3. Under **Servers**, choose **+ Add server** and enter the server's folder (the one with `valheim_server.exe` or `valheim_server.x86_64`). The app remembers it.
4. Pick the server, then a server pack, and choose **Install**.
5. Start the server yourself. On Linux, start it with `bash start_server_bepinex.sh` instead of `start_server.sh`; set the server name, world and password in that script. Your changes to it are kept when the pack is updated.

**Forget this server** only removes the folder from the app's list; the server and its pack stay as they are.

## Troubleshooting

- **Game not detected on Linux:** Valheim must run through Proton (Steam → Properties → Compatibility) and must have been started once.
- **Permission denied on Windows:** run the app as administrator.
- **Permission denied on Linux:** run the app as the user that owns the game or server folder.
- **Server mods not loading on Linux:** start the server with `bash start_server_bepinex.sh`.
- **"Leftover mod files found":** mods installed by hand earlier are in the way. Let the app delete them, or remove them yourself.
- **Something else:** the log file `crackers-modinst.log` is in your temp folder (`%TEMP%` on Windows, `/tmp` on Linux). Attach it when you report a problem.
