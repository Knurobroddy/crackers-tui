package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Knurobroddy/crackers-tui/internal/config"
)

const (
	maxPanelWidth = 96 // including border
	minPanelWidth = 30
	panelChrome   = 6 // border 2 + padding 4
	minBarWidth   = 10
	maxBarWidth   = 50
	barTextWidth  = 24 // room for the byte counts next to the bar
	byteUnit      = 1024
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

// View lays the screen out full-terminal: the banner and the panel with the
// current screen centered, the key help on the last line.
func (m *model) View() string {
	panel := panelStyle.Width(m.panelWidth() - 2).Render(m.body())
	help := lipgloss.PlaceHorizontal(m.width, lipgloss.Center, helpStyle.Render(m.help()))
	availableHeight := max(1, m.height-lipgloss.Height(help))

	version := "v" + m.deps.AppVersion
	if m.deps.AppVersion == config.DevVersion {
		version = "dev build"
	}
	head := header(version, m.width, availableHeight-lipgloss.Height(panel)-1)
	content := lipgloss.JoinVertical(lipgloss.Center, head, "", panel)
	return lipgloss.Place(m.width, availableHeight, lipgloss.Center, lipgloss.Center, content) + "\n" + help
}

// panelWidth is the panel's outer width.
func (m *model) panelWidth() int {
	return max(minPanelWidth, min(maxPanelWidth, m.width-2))
}

// innerWidth is the usable text width inside the panel.
func (m *model) innerWidth() int {
	return m.panelWidth() - panelChrome
}

// body is the content of the current screen.
func (m *model) body() string {
	var b strings.Builder
	wrap := lipgloss.NewStyle().Width(m.innerWidth())

	switch m.screen {
	case screenLoading:
		b.WriteString(m.spinner.View() + " " + m.loadingText)
	case screenError:
		b.WriteString(errStyle.Inherit(wrap).Render(m.errText))
		b.WriteString("\n\n" + m.form.View())
	case screenUpdating:
		b.WriteString(m.spinner.View() + " Updating " + config.AppName + "…")
		b.WriteString(m.pleaseWaitLine())
	case screenUpdated:
		b.WriteString(okStyle.Render("Updated — please restart " + config.AppName + "."))
		b.WriteString("\n\nPress any key to exit.")
	case screenMain:
		if len(m.games) == 0 {
			b.WriteString(m.noGamesView(wrap))
		}
		b.WriteString(m.form.View())
	case screenUpdatePrompt, screenGame, screenPacks, screenConfirm:
		b.WriteString(m.form.View())
	case screenProgress:
		b.WriteString(m.progressView())
	case screenResult:
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
	if m.library != nil {
		hasPacks := m.library.Index.GamesWithPacks()
		for _, game := range m.library.Games.Games {
			if !hasPacks[game.ID] {
				continue
			}
			line := "• " + boldStyle.Render(game.Name)
			if game.NotFoundHint != "" {
				line += "\n  " + game.NotFoundHint
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
	if download := m.download; download.Count > 0 {
		fmt.Fprintf(&b, "\n\nFile %d of %d: %s\n", download.Index, download.Count, download.File)
		if download.Total > 0 {
			m.bar.Width = max(minBarWidth, min(maxBarWidth, m.innerWidth()-barTextWidth))
			fraction := float64(download.Done) / float64(download.Total)
			b.WriteString(m.bar.ViewAs(min(1, fraction)))
			fmt.Fprintf(&b, "  %s / %s", humanBytes(download.Done), humanBytes(download.Total))
		} else {
			b.WriteString(humanBytes(download.Done))
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
	case screenLoading, screenUpdating, screenProgress:
		if m.busy {
			return "please wait…"
		}
		return "q quit"
	case screenUpdated:
		return "any key exit"
	case screenResult:
		return "enter continue • q quit"
	case screenMain, screenError:
		return "↑/↓ move • enter select • q quit"
	case screenUpdatePrompt:
		return "←/→ choose • enter confirm • q quit"
	case screenConfirm:
		return "←/→ choose • enter confirm • esc back • q quit"
	}
	return "↑/↓ move • enter select • esc back • q quit"
}

func humanBytes(n int64) string {
	if n < byteUnit {
		return fmt.Sprintf("%d B", n)
	}
	divisor, exponent := int64(byteUnit), 0
	for rest := n / byteUnit; rest >= byteUnit; rest /= byteUnit {
		divisor *= byteUnit
		exponent++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(divisor), "KMGTPE"[exponent])
}
