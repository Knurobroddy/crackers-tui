package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Knurobroddy/crackers-tui/internal/config"
)

// logoGap is the column gap between "CRACKERS" and "MODINST".
const logoGap = 4

// logoArt is the "CRACKERS MODINST" banner (125 columns wide).
var logoArt = []string{
	` ██████╗██████╗  █████╗  ██████╗██╗  ██╗███████╗██████╗ ███████╗    ███╗   ███╗ ██████╗ ██████╗ ██╗███╗   ██╗███████╗████████╗`,
	`██╔════╝██╔══██╗██╔══██╗██╔════╝██║ ██╔╝██╔════╝██╔══██╗██╔════╝    ████╗ ████║██╔═══██╗██╔══██╗██║████╗  ██║██╔════╝╚══██╔══╝`,
	`██║     ██████╔╝███████║██║     █████╔╝ █████╗  ██████╔╝███████╗    ██╔████╔██║██║   ██║██║  ██║██║██╔██╗ ██║███████╗   ██║`,
	`██║     ██╔══██╗██╔══██║██║     ██╔═██╗ ██╔══╝  ██╔══██╗╚════██║    ██║╚██╔╝██║██║   ██║██║  ██║██║██║╚██╗██║╚════██║   ██║`,
	`╚██████╗██║  ██║██║  ██║╚██████╗██║  ██╗███████╗██║  ██║███████║    ██║ ╚═╝ ██║╚██████╔╝██████╔╝██║██║ ╚████║███████║   ██║`,
	` ╚═════╝╚═╝  ╚═╝╚═╝  ╚═╝ ╚═════╝╚═╝  ╚═╝╚══════╝╚═╝  ╚═╝╚══════╝    ╚═╝     ╚═╝ ╚═════╝ ╚═════╝ ╚═╝╚═╝  ╚═══╝╚══════╝   ╚═╝`,
}

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
		runes := []rune(line)
		crackers = append(crackers, strings.TrimRight(string(runes[:split]), " "))
		if len(runes) > split+logoGap {
			modinst = append(modinst, string(runes[split+logoGap:]))
		} else {
			modinst = append(modinst, "")
		}
	}
	return crackers, modinst
}

// header returns the largest banner that fits: the full one-line art, the art
// stacked as CRACKERS over MODINST, or plain text. The version goes below.
func header(version string, width, maxHeight int) string {
	crackers := crackersStyle.Render(strings.Join(logoCrackers, "\n"))
	modinst := modinstStyle.Render(strings.Join(logoModinst, "\n"))
	versionLine := helpStyle.Render(version)

	wide := lipgloss.JoinVertical(lipgloss.Center, lipgloss.JoinHorizontal(lipgloss.Top, crackers, strings.Repeat(" ", logoGap), modinst), versionLine)
	if lipgloss.Width(wide) <= width-2 && lipgloss.Height(wide) <= maxHeight {
		return wide
	}
	stacked := lipgloss.JoinVertical(lipgloss.Center, crackers, "", modinst, versionLine)
	if lipgloss.Width(stacked) <= width-2 && lipgloss.Height(stacked) <= maxHeight {
		return stacked
	}
	return titleStyle.Render(config.AppName + " " + version)
}
