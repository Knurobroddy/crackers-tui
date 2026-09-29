# Crackers Modinst

A desktop tool that installs and removes modpacks from a remote library into games and dedicated servers found on the player's machine.

## Language

### Install targets

**Target**:
Anything a pack can be installed into: one entry in the library's target list, detected on the machine by its own rules. Every target is either a Game or a Server.
_Avoid_: Game entry (when servers are meant too), install

**Game**:
A target the player runs to play (for example the Valheim client).
_Avoid_: Client (except in pack names)

**Server**:
A target that hosts a world for other players (for example the Valheim Dedicated Server). Shown to the player separately from games, but otherwise handled like one.
_Avoid_: Dedicated game, host

**Server folder**:
The folder a server is installed in. The player gives it to the tool; the tool never searches for it.
_Avoid_: Server path, server dir

**Saved server**:
A server folder the tool remembers between runs so the player does not enter it again.
_Avoid_: Registered server, known server

**Forget**:
Stop remembering a saved server. Leaves the server folder and any installed pack untouched.
_Avoid_: Remove server, delete server (Remove means removing a pack)

### Packs

**Pack**:
A named, versioned set of mods and files from the library, built for exactly one target.
_Avoid_: Modpack (in code), profile
