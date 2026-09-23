package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Knurobroddy/crackers-tui/internal/config"
)

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("62")).Padding(0, 1)
	accentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
	helpStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	boldStyle   = lipgloss.NewStyle().Bold(true)
	panelStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("62")).Padding(1, 2)
)

const maxPanelWidth = 96 // including border

// panelWidth is the panel's outer width.
func (m *model) panelWidth() int {
	return max(30, min(maxPanelWidth, m.width-2))
}

// innerWidth is the usable text width inside the panel (border 2 + padding 4).
func (m *model) innerWidth() int {
	return m.panelWidth() - 6
}

// View lays the screen out full-terminal: the banner and the panel with the
// current screen centered, the key help on the last line.
func (m *model) View() string {
	panel := panelStyle.Width(m.panelWidth() - 2).Render(m.body())
	help := lipgloss.PlaceHorizontal(m.width, lipgloss.Center, helpStyle.Render(m.help()))
	avail := max(1, m.height-lipgloss.Height(help))

	version := "v" + m.d.AppVersion
	if m.d.AppVersion == config.DevVersion {
		version = "dev build"
	}
	head := header(version, m.width, avail-lipgloss.Height(panel)-1)
	content := lipgloss.JoinVertical(lipgloss.Center, head, "", panel)
	return lipgloss.Place(m.width, avail, lipgloss.Center, lipgloss.Center, content) + "\n" + help
}

// body is the content of the current screen.
func (m *model) body() string {
	var b strings.Builder
	wrap := lipgloss.NewStyle().Width(m.innerWidth())

	switch m.screen {
	case scrLoading:
		b.WriteString(m.spinner.View() + " " + m.loadingText)
	case scrError:
		b.WriteString(errStyle.Inherit(wrap).Render(m.errText))
		b.WriteString("\n\n" + m.form.View())
	case scrUpdating:
		b.WriteString(m.spinner.View() + " Updating " + config.AppName + "…")
		b.WriteString(m.pleaseWaitLine())
	case scrUpdated:
		b.WriteString(okStyle.Render("Updated — please restart " + config.AppName + "."))
		b.WriteString("\n\nPress any key to exit.")
	case scrMain:
		if len(m.rows) == 0 {
			b.WriteString(m.noGamesView(wrap))
		}
		b.WriteString(m.form.View())
	case scrUpdatePrompt, scrGame, scrPacks, scrConfirm:
		b.WriteString(m.form.View())
	case scrProgress:
		b.WriteString(m.progressView())
	case scrResult:
		if m.resultOK {
			b.WriteString(okStyle.Render("Done."))
			b.WriteString("\n\n" + wrap.Render(m.resultText))
		} else {
			b.WriteString(errStyle.Bold(true).Render("Something went wrong."))
			b.WriteString("\n\n" + errStyle.Inherit(wrap).Render(m.resultText))
		}
		b.WriteString("\n\n" + boldStyle.Render("Press enter to continue."))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *model) noGamesView(wrap lipgloss.Style) string {
	var b strings.Builder
	b.WriteString(warnStyle.Render("No supported games were detected."))
	b.WriteString("\n\nSupported games:\n")
	hasPacks := map[string]bool{}
	if m.index != nil {
		hasPacks = m.index.GamesWithPacks()
	}
	if m.games != nil {
		for _, g := range m.games.Games {
			if !hasPacks[g.ID] {
				continue
			}
			line := "• " + boldStyle.Render(g.Name)
			if g.NotFoundHint != "" {
				line += "\n  " + g.NotFoundHint
			}
			b.WriteString(wrap.Render(line) + "\n")
		}
	}
	b.WriteString("\n")
	return b.String()
}

func (m *model) progressView() string {
	var b strings.Builder
	b.WriteString(m.spinner.View() + " " + m.stepText)
	if dl := m.dl; dl.Count > 0 {
		b.WriteString(fmt.Sprintf("\n\nFile %d of %d: %s\n", dl.Index, dl.Count, dl.File))
		if dl.Total > 0 {
			m.bar.Width = max(10, min(50, m.innerWidth()-24))
			pct := float64(dl.Done) / float64(dl.Total)
			b.WriteString(m.bar.ViewAs(min(1, pct)))
			b.WriteString(fmt.Sprintf("  %s / %s", humanBytes(dl.Done), humanBytes(dl.Total)))
		} else {
			b.WriteString(humanBytes(dl.Done))
		}
	}
	b.WriteString(m.pleaseWaitLine())
	return b.String()
}

func (m *model) pleaseWaitLine() string {
	if !m.pleaseWait {
		return ""
	}
	return "\n\n" + warnStyle.Render("Please wait — "+config.AppName+" cannot quit until this step has finished.")
}

func (m *model) help() string {
	switch m.screen {
	case scrLoading, scrUpdating, scrProgress:
		if m.busy {
			return "please wait…"
		}
		return "q quit"
	case scrUpdated:
		return "any key exit"
	case scrResult:
		return "enter continue • q quit"
	case scrMain, scrError:
		return "↑/↓ move • enter select • q quit"
	case scrUpdatePrompt:
		return "←/→ choose • enter confirm • q quit"
	case scrConfirm:
		return "←/→ choose • enter confirm • esc back • q quit"
	}
	return "↑/↓ move • enter select • esc back • q quit"
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
