package tui

import (
	"fmt"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"github.com/Knurobroddy/crackers-tui/internal/config"
	"github.com/Knurobroddy/crackers-tui/internal/engine"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
)

func keyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit.SetEnabled(false)          // q / ctrl+c are handled by the model
	km.Select.Filter.SetEnabled(false) // short menus; "/" would only confuse
	return km
}

func selectForm(title, desc string, opts ...huh.Option[string]) *huh.Form {
	s := huh.NewSelect[string]().Key("choice").Title(title).Options(opts...)
	if desc != "" {
		s.Description(desc)
	}
	return huh.NewForm(huh.NewGroup(s)).WithShowHelp(false).WithKeyMap(keyMap()).WithTheme(huh.ThemeCharm())
}

func confirmForm(title, desc string) *huh.Form {
	c := huh.NewConfirm().Key("confirm").Title(title).Affirmative("Yes").Negative("No")
	if desc != "" {
		c.Description(desc)
	}
	return huh.NewForm(huh.NewGroup(c)).WithShowHelp(false).WithKeyMap(keyMap()).WithTheme(huh.ThemeCharm())
}

func (m *model) setForm(f *huh.Form) tea.Cmd {
	m.form = f.WithWidth(m.innerWidth())
	return m.form.Init()
}

// formDone handles a completed form on the current screen.
func (m *model) formDone() tea.Cmd {
	f := m.form
	switch m.screen {
	case scrError:
		if f.GetString("choice") != "retry" {
			return tea.Quit
		}
		if m.errKind == errUpdate {
			return m.startUpdate()
		}
		return m.reload()

	case scrUpdatePrompt:
		if f.GetBool("confirm") {
			return m.startUpdate()
		}
		m.updateDeclined = true
		if remote.IsUpdateRequired(m.remoteErr) {
			return tea.Quit
		}
		return m.proceed()

	case scrMain:
		switch c := f.GetString("choice"); c {
		case "detect":
			return m.reload() // fresh library + detection
		case "quit":
			return tea.Quit
		default:
			i, err := strconv.Atoi(c)
			if err != nil || i < 0 || i >= len(m.rows) {
				return m.openMain()
			}
			m.cur = i
			return m.openGame()
		}

	case scrGame:
		row := m.rows[m.cur]
		switch f.GetString("choice") {
		case "install":
			m.packsAction = actInstall
			return m.openPacks()
		case "cleanup":
			if packs := m.index.PacksFor(row.Game.ID); len(packs) == 1 {
				return m.openConfirm(actCleanup, packs[0], scrGame)
			}
			m.packsAction = actCleanup
			return m.openPacks()
		case "remove":
			return m.openConfirm(actRemove, row.Status.Pack, scrGame)
		case "reinstall":
			return m.openConfirm(actReinstall, row.Status.Pack, scrGame)
		}
		return m.openMain()

	case scrPacks:
		id := f.GetString("choice")
		pack, ok := m.index.Pack(id)
		if !ok {
			return m.openGame()
		}
		if m.packsAction == actCleanup {
			return m.openConfirm(actCleanup, pack, scrPacks)
		}
		if mk := m.rows[m.cur].Status.Marker; mk != nil && mk.PackID == pack.ID {
			return m.openConfirm(actReinstall, pack, scrPacks)
		}
		return m.openConfirm(actInstall, pack, scrPacks)

	case scrConfirm:
		if f.GetBool("confirm") {
			return m.startOp()
		}
		cmd, _ := m.back()
		return cmd
	}
	return nil
}

func (m *model) reload() tea.Cmd {
	m.screen = scrLoading
	m.form = nil
	m.loadingText = "Loading the modpack library…"
	m.remoteDone, m.detectDone = false, false
	m.remoteErr = nil
	return m.loadRemoteCmd()
}

func (m *model) startUpdate() tea.Cmd {
	m.screen = scrUpdating
	m.form = nil
	m.busy = true
	return m.applyUpdateCmd()
}

func (m *model) openUpdatePrompt(forced bool) tea.Cmd {
	m.screen = scrUpdatePrompt
	desc := ""
	if forced {
		desc = "The modpack library requires a newer version. Choosing No quits."
	}
	return m.setForm(confirmForm(fmt.Sprintf("Update %s to v%s?", config.AppName, m.updateRel.Version), desc))
}

func (m *model) rowLabel(i int) string {
	r := m.rows[i]
	label := fmt.Sprintf("%s — %s", r.Game.Name, r.Status)
	for j, other := range m.rows {
		if j != i && other.Game.ID == r.Game.ID {
			return label + "  (" + r.Result.RootDir + ")"
		}
	}
	return label
}

func (m *model) openMain() tea.Cmd {
	m.screen = scrMain
	var opts []huh.Option[string]
	for i := range m.rows {
		opts = append(opts, huh.NewOption(m.rowLabel(i), strconv.Itoa(i)))
	}
	opts = append(opts, huh.NewOption("Detect again", "detect"), huh.NewOption("Quit", "quit"))
	title := "Choose a game"
	if len(m.rows) == 0 {
		title = "What now?"
	}
	return m.setForm(selectForm(title, "", opts...))
}

func (m *model) openGame() tea.Cmd {
	m.screen = scrGame
	row := m.rows[m.cur]
	packs := m.index.PacksFor(row.Game.ID)
	var opts []huh.Option[string]
	switch row.Status.State {
	case engine.NotInstalled:
		opts = append(opts, huh.NewOption("Install pack", "install"))
		if len(packs) > 0 {
			opts = append(opts, huh.NewOption("Remove leftover mod files", "cleanup"))
		}
	case engine.Installed, engine.UpdateAvailable:
		opts = append(opts,
			huh.NewOption("Remove pack", "remove"),
			huh.NewOption("Reinstall / Update", "reinstall"))
		if len(packs) > 1 {
			opts = append(opts, huh.NewOption("Install a different pack", "install"))
		}
	case engine.NotOffered:
		opts = append(opts, huh.NewOption("Remove pack", "remove"))
		if len(packs) > 0 {
			opts = append(opts, huh.NewOption("Install a different pack", "install"))
		}
	default:
		opts = append(opts, huh.NewOption("Remove pack", "remove"))
	}
	opts = append(opts, huh.NewOption("Back", "back"))

	desc := "Folder: " + row.Result.RootDir + "\nStatus: " + row.Status.String()
	if row.Status.Err != nil {
		desc += "\n" + row.Status.Err.Error()
	}
	return m.setForm(selectForm(row.Game.Name, desc, opts...))
}

func (m *model) openPacks() tea.Cmd {
	m.screen = scrPacks
	row := m.rows[m.cur]
	var opts []huh.Option[string]
	for _, p := range m.index.PacksFor(row.Game.ID) {
		label := p.Name
		if p.Description != "" {
			label += " — " + p.Description
		}
		if mk := row.Status.Marker; mk != nil && mk.PackID == p.ID {
			label += "  (installed)"
		}
		opts = append(opts, huh.NewOption(label, p.ID))
	}
	opts = append(opts, huh.NewOption("Back", "back"))
	title, desc := "Choose a pack for "+row.Game.Name, ""
	if m.packsAction == actCleanup {
		title, desc = "Remove leftovers of which pack?", "Only files and folders that this pack installs are looked at."
	}
	return m.setForm(selectForm(title, desc, opts...))
}

func (m *model) openConfirm(kind action, pack remote.PackRef, back screen) tea.Cmd {
	row := m.rows[m.cur]
	m.op = pendingOp{kind: kind, row: row, pack: pack}
	m.confirmBack = back
	current := "the installed pack"
	if mk := row.Status.Marker; mk != nil {
		current = mk.PackName
	}
	var title, desc string
	switch kind {
	case actInstall:
		title = fmt.Sprintf("Install %s?", pack.Name)
		desc = "Into: " + row.Result.RootDir
		if row.Status.State != engine.NotInstalled {
			desc = fmt.Sprintf("This will remove %s first.\n%s", current, desc)
		}
	case actReinstall:
		title = fmt.Sprintf("Reinstall %s?", pack.Name)
		desc = "The pack is replaced by its latest version. Mod settings you changed are kept where the pack preserves them " +
			"(e.g. BepInEx/config).\nFolder: " + row.Result.RootDir
	case actCleanup:
		title = "Remove leftover mod files?"
		desc = fmt.Sprintf("Deletes files and folders in %s that %s installs (e.g. left over from an earlier manual mod install). "+
			"Nothing else is touched. The pack is downloaded first to know its files.", row.Result.RootDir, pack.Name)
	case actRemove:
		title = fmt.Sprintf("Remove %s?", current)
		desc = "From: " + row.Result.RootDir + "\nFiles the mods created in the pack's folders (configs, logs) are deleted too."
	}
	m.screen = scrConfirm
	return m.setForm(confirmForm(title, desc))
}

// openLeftoversConfirm asks whether to delete leftovers that blocked an install
// and retry it.
func (m *model) openLeftoversConfirm(lo *engine.LeftoversError) tea.Cmd {
	m.op.clean = true
	m.confirmBack = scrGame
	m.screen = scrConfirm
	desc := fmt.Sprintf("These already exist in %s, probably from an earlier mod install:\n%s\n\nThey will be deleted, then %s is installed.",
		lo.Root, listPaths(lo.Paths, 8), m.op.pack.Name)
	return m.setForm(confirmForm("Leftover mod files found. Delete them and install?", desc))
}
