package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Knurobroddy/crackers-tui/internal/config"
)

// logoArt is the "CRACKERS MODINST" banner (125 columns wide).
var logoArt = []string{
	` ██████╗██████╗  █████╗  ██████╗██╗  ██╗███████╗██████╗ ███████╗    ███╗   ███╗ ██████╗ ██████╗ ██╗███╗   ██╗███████╗████████╗`,
	`██╔════╝██╔══██╗██╔══██╗██╔════╝██║ ██╔╝██╔════╝██╔══██╗██╔════╝    ████╗ ████║██╔═══██╗██╔══██╗██║████╗  ██║██╔════╝╚══██╔══╝`,
	`██║     ██████╔╝███████║██║     █████╔╝ █████╗  ██████╔╝███████╗    ██╔████╔██║██║   ██║██║  ██║██║██╔██╗ ██║███████╗   ██║`,
	`██║     ██╔══██╗██╔══██║██║     ██╔═██╗ ██╔══╝  ██╔══██╗╚════██║    ██║╚██╔╝██║██║   ██║██║  ██║██║██║╚██╗██║╚════██║   ██║`,
	`╚██████╗██║  ██║██║  ██║╚██████╗██║  ██╗███████╗██║  ██║███████║    ██║ ╚═╝ ██║╚██████╔╝██████╔╝██║██║ ╚████║███████║   ██║`,
	` ╚═════╝╚═╝  ╚═╝╚═╝  ╚═╝ ╚═════╝╚═╝  ╚═╝╚══════╝╚═╝  ╚═╝╚══════╝    ╚═╝     ╚═╝ ╚═════╝ ╚═════╝ ╚═╝╚═╝  ╚═══╝╚══════╝   ╚═╝`,
}

// logoGap is the 4-column gap between "CRACKERS" and "MODINST".
const logoGap = 4

var (
	crackersStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#E8A33D"))
	modinstStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#8B6CF6"))
)

// logoCrackers and logoModinst are the banner's two blocks, split once.
var logoCrackers, logoModinst = logoParts()

// logoParts splits the banner into its "CRACKERS" and "MODINST" blocks.
func logoParts() (crackers, modinst []string) {
	split := len([]rune(logoArt[0][:strings.Index(logoArt[0], "    ███╗")]))
	for _, line := range logoArt {
		r := []rune(line)
		crackers = append(crackers, strings.TrimRight(string(r[:split]), " "))
		if len(r) > split+logoGap {
			modinst = append(modinst, string(r[split+logoGap:]))
		} else {
			modinst = append(modinst, "")
		}
	}
	return crackers, modinst
}

// header returns the largest banner that fits: the full one-line art, the art
// stacked as CRACKERS over MODINST, or plain text. The version goes below.
func header(version string, width, maxHeight int) string {
	c := crackersStyle.Render(strings.Join(logoCrackers, "\n"))
	mo := modinstStyle.Render(strings.Join(logoModinst, "\n"))
	ver := helpStyle.Render(version)

	wide := lipgloss.JoinVertical(lipgloss.Center, lipgloss.JoinHorizontal(lipgloss.Top, c, strings.Repeat(" ", logoGap), mo), ver)
	if lipgloss.Width(wide) <= width-2 && lipgloss.Height(wide) <= maxHeight {
		return wide
	}
	stacked := lipgloss.JoinVertical(lipgloss.Center, c, "", mo, ver)
	if lipgloss.Width(stacked) <= width-2 && lipgloss.Height(stacked) <= maxHeight {
		return stacked
	}
	return titleStyle.Render(config.AppName + " " + version)
}
