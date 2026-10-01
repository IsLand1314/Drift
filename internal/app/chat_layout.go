package app

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const fixedFooterRows = 3

// fixedChatFooter reserves the last three terminal rows while a turn runs.
// It relies on the terminal's native scroll region, so non-TTY output is unchanged.
type fixedChatFooter struct {
	out           io.Writer
	modelName     string
	width         int
	height        int
	contentBottom int
	active        bool
}

func newFixedChatFooter(out io.Writer, input *ttyChatInput) *fixedChatFooter {
	if input == nil {
		return nil
	}
	return &fixedChatFooter{out: out, modelName: input.modelName, width: input.width, height: input.height}
}

func (f *fixedChatFooter) Begin() {
	if f == nil || !chatUsesColor(f.out) || f.height < fixedFooterRows+4 || f.width < 1 {
		return
	}
	f.contentBottom = f.height - fixedFooterRows
	f.active = true
	_, _ = io.WriteString(f.out, fixedFooterBeginSequence(f.width, f.height, f.contentBottom, f.modelName))
}

func (f *fixedChatFooter) End() {
	if f == nil || !f.active {
		return
	}
	_, _ = io.WriteString(f.out, fixedFooterEndSequence(f.contentBottom))
	f.active = false
}

func fixedFooterBeginSequence(width, height, contentBottom int, modelName string) string {
	left := "  Enter 发送 · Ctrl+C 取消"
	right := modelName
	spaces := width - displayWidth(left) - displayWidth(right)
	if right == "" || spaces < 1 {
		right = ""
		spaces = 1
	}
	muted := "\x1b[2m"
	reset := "\x1b[0m"
	separator := "────────────────────────"
	footer := muted + separator + reset + "\x1b[" + fmt.Sprint(height-1) + ";1H" + muted + left + spacesString(spaces) + right + reset + "\x1b[" + fmt.Sprint(height) + ";1H" + muted + separator + reset
	return "\x1b7\x1b[1;" + fmt.Sprint(contentBottom) + "r\x1b[" + fmt.Sprint(contentBottom+1) + ";1H\x1b[0J" + footer + "\x1b8\r\n"
}

func fixedFooterEndSequence(contentBottom int) string {
	return "\x1b7\x1b[r\x1b[" + fmt.Sprint(contentBottom+1) + ";1H\x1b[0J\x1b8"
}

func spacesString(n int) string {
	if n < 1 {
		return " "
	}
	return strings.Repeat(" ", n)
}

func displayWidth(s string) int {
	return lipgloss.Width(s)
}
