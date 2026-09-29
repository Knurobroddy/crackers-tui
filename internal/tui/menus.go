package tui

import (
	"fmt"
	"slices"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"github.com/Knurobroddy/crackers-tui/internal/app"
	"github.com/Knurobroddy/crackers-tui/internal/config"
	"github.com/Knurobroddy/crackers-tui/internal/engine"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
)

func keyMap() *huh.KeyMap {
	keys := huh.NewDefaultKeyMap()
	keys.Quit.SetEnabled(false)          // q / ctrl+c are handled by the model
	keys.Select.Filter.SetEnabled(false) // short menus; "/" would only confuse
	return keys
}

func selectForm(title, description string, options ...huh.Option[string]) *huh.Form {
	field := huh.NewSelect[string]().Key("choice").Title(title).Options(options...)
	if description != "" {
		field.Description(description)
	}
	return huh.NewForm(huh.NewGroup(field)).WithShowHelp(false).WithKeyMap(keyMap()).WithTheme(huh.ThemeCharm())
}

func confirmForm(title, description string) *huh.Form {
	field := huh.NewConfirm().Key("confirm").Title(title).Affirmative("Yes").Negative("No")
	if description != "" {
		field.Description(description)
	}
	return huh.NewForm(huh.NewGroup(field)).WithShowHelp(false).WithKeyMap(keyMap()).WithTheme(huh.ThemeCharm())
}

func (m *model) setForm(form *huh.Form) tea.Cmd {
	m.form = form.WithWidth(m.innerWidth())
	return m.form.Init()
}

func (m *model) openUpdatePrompt(forced bool) tea.Cmd {
	m.screen = screenUpdatePrompt
	description := ""
	if forced {
		description = "The modpack library requires a newer version. Choosing No quits."
	}
	return m.setForm(confirmForm(fmt.Sprintf("Update %s to v%s?", config.AppName, m.updateRelease.Version), description))
}

// gameLabel adds the folder when the same game is installed more than once,
// and always for a server, since admins often run several.
func (m *model) gameLabel(i int) string {
	game := m.games[i]
	label := fmt.Sprintf("%s — %s", game.Def.Name, game.Status)
	withFolder := label + "  (" + game.Install.RootDir + ")"
	if game.Def.IsServer() {
		return withFolder
	}
	for j, other := range m.games {
		if j != i && other.Def.ID == game.Def.ID {
			return withFolder
		}
	}
	return label
}

func (m *model) openMain() tea.Cmd {
	m.screen = screenMain
	title := "Choose a game"
	switch {
	case m.showsServers():
		title = "Choose a game or server"
	case len(m.games) == 0:
		title = "What now?"
	}
	options := m.mainOptions()
	cmd := m.setForm(selectForm(title, "", options...))
	if len(options) > 0 && options[0].Value == choiceHeader {
		// Step off the header by key: huh scrolls a preselected option to the
		// top, which would hide the header above it.
		m.form.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	return cmd
}

// showsServers reports whether the main menu has a Servers section: the
// library offers a server pack, or servers were saved.
func (m *model) showsServers() bool {
	if len(m.missingServers) > 0 || m.serversErr != nil {
		return true
	}
	if m.library != nil && len(m.library.ServerDefs()) > 0 {
		return true
	}
	return slices.ContainsFunc(m.games, isServer)
}

// mainOptions lists games, then servers, then the fixed actions. Without
// servers the menu stays a plain game list.
func (m *model) mainOptions() []huh.Option[string] {
	actions := []huh.Option[string]{huh.NewOption("Detect again", choiceDetect), huh.NewOption("Quit", choiceQuit)}
	if !m.showsServers() {
		return append(m.gameRows(""), actions...)
	}
	var options []huh.Option[string]
	if games := m.gameRows(rowIndent); len(games) > 0 {
		options = append(options, sectionHeader("Games"))
		options = append(options, games...)
	}
	options = append(options, sectionHeader("Servers"))
	options = append(options, m.serverRows()...)
	if m.library != nil && len(m.library.ServerDefs()) > 0 {
		options = append(options, huh.NewOption(rowIndent+"+ Add server", choiceAddServer))
	}
	return append(options, actions...)
}

func (m *model) gameRows(indent string) []huh.Option[string] {
	var options []huh.Option[string]
	for i, game := range m.games {
		if !game.Def.IsServer() {
			options = append(options, huh.NewOption(indent+m.gameLabel(i), strconv.Itoa(i)))
		}
	}
	return options
}

func (m *model) serverRows() []huh.Option[string] {
	var options []huh.Option[string]
	for i, game := range m.games {
		if game.Def.IsServer() {
			options = append(options, huh.NewOption(rowIndent+m.gameLabel(i), strconv.Itoa(i)))
		}
	}
	for i, dir := range m.missingServers {
		label := fmt.Sprintf("%sSaved server — Not found  (%s)", rowIndent, dir)
		options = append(options, huh.NewOption(label, missingPrefix+strconv.Itoa(i)))
	}
	return options
}

// sectionHeader is a menu row that only labels the rows below it; choosing
// it reopens the menu.
func sectionHeader(title string) huh.Option[string] {
	return huh.NewOption("── "+title+" ──", choiceHeader)
}

func isServer(game app.Game) bool { return game.Def.IsServer() }

func isGame(game app.Game) bool { return !game.Def.IsServer() }

func (m *model) openAddServer() tea.Cmd {
	m.screen = screenAddServer
	field := huh.NewInput().Key("folder").Title("Add a server").
		Description("Enter the folder the dedicated server is installed in (the one with the server's program in it).").
		Placeholder("server folder")
	form := huh.NewForm(huh.NewGroup(field)).WithShowHelp(false).WithKeyMap(keyMap()).WithTheme(huh.ThemeCharm())
	return m.setForm(form)
}

// openForget asks whether to forget the saved server folder dir; esc and No
// return to back.
func (m *model) openForget(dir string, back screen) tea.Cmd {
	m.forgetDir, m.forgetBack = dir, back
	m.screen = screenForget
	description := "Folder: " + dir + "\nThe folder and any pack installed in it are left as they are; " +
		"the server is only removed from this list."
	return m.setForm(confirmForm("Forget this server?", description))
}

func (m *model) openGame() tea.Cmd {
	m.screen = screenGame
	game := m.games[m.selected]
	description := "Folder: " + game.Install.RootDir + "\nStatus: " + game.Status.String()
	if game.Status.Err != nil {
		description += "\n" + app.UserMessage(game.Status.Err)
	}
	return m.setForm(selectForm(game.Def.Name, description, m.gameOptions(game)...))
}

func (m *model) gameOptions(game app.Game) []huh.Option[string] {
	packs := m.library.Index.PacksFor(game.Def.ID)
	var options []huh.Option[string]
	switch game.Status.State {
	case engine.NotInstalled:
		options = append(options, huh.NewOption("Install pack", "install"))
		if len(packs) > 0 {
			options = append(options, huh.NewOption("Remove leftover mod files", "cleanup"))
		}
	case engine.Installed, engine.UpdateAvailable:
		options = append(options,
			huh.NewOption("Remove pack", "remove"),
			huh.NewOption("Reinstall / Update", "reinstall"))
		if len(packs) > 1 {
			options = append(options, huh.NewOption("Install a different pack", "install"))
		}
	case engine.NotOffered:
		options = append(options, huh.NewOption("Remove pack", "remove"))
		if len(packs) > 0 {
			options = append(options, huh.NewOption("Install a different pack", "install"))
		}
	default:
		options = append(options, huh.NewOption("Remove pack", "remove"))
	}
	if game.Def.IsServer() {
		options = append(options, huh.NewOption("Forget this server", "forget"))
	}
	return append(options, huh.NewOption("Back", "back"))
}

func (m *model) openPacks() tea.Cmd {
	m.screen = screenPacks
	game := m.games[m.selected]
	var options []huh.Option[string]
	for _, pack := range m.library.Index.PacksFor(game.Def.ID) {
		label := pack.Name
		if pack.Description != "" {
			label += " — " + pack.Description
		}
		if marker := game.Status.Marker; marker != nil && marker.PackID == pack.ID {
			label += "  (installed)"
		}
		options = append(options, huh.NewOption(label, pack.ID))
	}
	options = append(options, huh.NewOption("Back", "back"))
	title, description := "Choose a pack for "+game.Def.Name, ""
	if m.packsKind == app.OpCleanLeftovers {
		title, description = "Remove leftovers of which pack?", "Only files and folders that this pack installs are looked at."
	}
	return m.setForm(selectForm(title, description, options...))
}

func (m *model) openConfirm(kind app.OpKind, pack remote.PackRef, back screen) tea.Cmd {
	game := m.games[m.selected]
	m.op = app.Operation{Kind: kind, Game: game, Pack: pack}
	m.confirmBack = back
	m.screen = screenConfirm
	title, description := confirmTexts(kind, game, pack)
	return m.setForm(confirmForm(title, description))
}

// openLeftoversConfirm asks whether to delete leftovers that blocked an install
// and retry it.
func (m *model) openLeftoversConfirm(leftoversErr *engine.LeftoversError) tea.Cmd {
	m.op.CleanLeftovers = true
	m.confirmBack = screenGame
	m.screen = screenConfirm
	description := fmt.Sprintf("These already exist in %s, probably from an earlier mod install:\n%s\n\nThey will be deleted, then %s is installed.",
		leftoversErr.Root, app.ListPaths(leftoversErr.Paths, maxLeftoversShown), m.op.Pack.Name)
	return m.setForm(confirmForm("Leftover mod files found. Delete them and install?", description))
}

func confirmTexts(kind app.OpKind, game app.Game, pack remote.PackRef) (title, description string) {
	title, description = operationTexts(kind, game, pack)
	if game.Def.IsServer() {
		description += "\nStop the server first."
	}
	return title, description
}

func operationTexts(kind app.OpKind, game app.Game, pack remote.PackRef) (title, description string) {
	root := game.Install.RootDir
	current := "the installed pack"
	if marker := game.Status.Marker; marker != nil {
		current = marker.PackName
	}
	switch kind {
	case app.OpInstall:
		description = "Into: " + root
		if game.Status.State != engine.NotInstalled {
			description = fmt.Sprintf("This will remove %s first.\n%s", current, description)
		}
		return fmt.Sprintf("Install %s?", pack.Name), description
	case app.OpReinstall:
		return fmt.Sprintf("Reinstall %s?", pack.Name),
			"The pack is replaced by its latest version. Mod settings you changed are kept where the pack preserves them " +
				"(e.g. BepInEx/config).\nFolder: " + root
	case app.OpCleanLeftovers:
		return "Remove leftover mod files?",
			fmt.Sprintf("Deletes files and folders in %s that %s installs (e.g. left over from an earlier manual mod install). "+
				"Nothing else is touched. The pack is downloaded first to know its files.", root, pack.Name)
	case app.OpRemove:
		return fmt.Sprintf("Remove %s?", current),
			"From: " + root + "\nFiles the mods created in the pack's folders (configs, logs) are deleted too."
	}
	return "", ""
}
