package review

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// ANSI escape codes.
const (
	ansiReset     = "\033[0m"
	ansiBold      = "\033[1m"
	ansiDim       = "\033[2m"
	ansiRed       = "\033[31m"
	ansiGreen     = "\033[32m"
	ansiYellow    = "\033[33m"
	ansiBlue      = "\033[34m"
	ansiCyan      = "\033[36m"
	ansiGray      = "\033[90m"
	ansiBoldRed   = "\033[1;31m"
	ansiBoldYellow = "\033[1;33m"
)

// colorsEnabled returns true if ANSI color output should be used.
func colorsEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return true
}

// stdoutIsTTY returns true if stdout is a terminal.
func stdoutIsTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// stderrIsTTY returns true if stderr is a terminal.
func stderrIsTTY() bool {
	return term.IsTerminal(int(os.Stderr.Fd()))
}

// terminalWidth returns the width of stdout, or fallback if not a TTY.
func terminalWidth(fallback int) int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		return fallback
	}
	return w
}

// colorize wraps text in ANSI color codes if colors are enabled.
func colorize(color, text string) string {
	if !colorsEnabled() {
		return text
	}
	return color + text + ansiReset
}

// severityColor returns the ANSI color code for a severity level.
func severityColor(severity string) string {
	switch strings.ToLower(severity) {
	case "critical":
		return ansiBoldRed
	case "bug":
		return ansiBoldYellow
	case "warning":
		return ansiYellow
	case "suggestion":
		return ansiBlue
	case "nitpick":
		return ansiGray
	default:
		return ansiBlue
	}
}

// severityDot returns a colored bullet for terminal display.
func severityDot(severity string) string {
	return colorize(severityColor(severity), "●")
}

// Exported color aliases for use by the CLI package.
const (
	ColorCyan   = ansiCyan
	ColorYellow = ansiYellow
	ColorGreen  = ansiGreen
	ColorBold   = ansiBold
	ColorDim    = ansiDim
	ColorRed    = ansiRed
)

// Stderrf prints a formatted message to stderr with optional color.
func Stderrf(color, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if stderrIsTTY() && colorsEnabled() {
		fmt.Fprintf(os.Stderr, "%s%s%s", color, msg, ansiReset)
	} else {
		fmt.Fprint(os.Stderr, msg)
	}
}
