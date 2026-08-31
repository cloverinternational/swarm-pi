package chat

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// app_chat_render_user.go
// User message rendering: bash commands, attachments, images, edit mode

// renderUserMessage renders a user message with a solid full-width background bar.
// Each line is padded to the terminal width and wrapped with reapplyBackground —
// the same proven pattern used throughout this codebase — so the elevated
// surface color (BGLight) fills edge-to-edge like the YOLO/permission badges.
func (a *App) renderUserMessage(
	msg Message,
	ctx MessageRenderContext,
	msgIdx int,
	msgStartIdx int,
	isFocused bool,
	isEditTarget bool,
	isDimmed bool,
	totalMessages int,
) renderedImageBlock {
	th := a.theme
	width := ctx.Width
	var msgLines []string
	var imageAnchors []nativeImageAnchor

	// Background: use the dedicated user-message surface so it's clearly distinct from BG.
	// Fall back to BGLight if UserMsgBG not set (e.g. custom/programmatic themes).
	userBG := th.UserMsgBG
	if userBG == "" {
		userBG = th.BGLight
	}
	focusBG := th.UserMsgBGFocused
	if focusBG == "" {
		focusBG = th.BGLighter
	}

	msgBG := userBG
	if isFocused {
		msgBG = focusBG
	} else if isDimmed {
		msgBG = th.BG
	}

	// Foreground style (no background set here — bg comes from reapplyBackground).
	fgStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text))
	if isDimmed {
		fgStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Strikethrough(true)
	} else if isEditTarget {
		fgStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).Bold(true)
	}

	// solidLine: render styled text, pad to full terminal width, then apply the
	// solid background via reapplyBackground.  visLen is the visual character
	// count of `text` (excluding any ANSI codes already in it).
	// Each line ends with \x1b[0m so the user-message bg attribute doesn't
	// leak into the next row (otherwise the next row's text cells inherit
	// UserMsgBG until something else resets — visible as a small dark stripe
	// behind e.g. "◇ Thinking" on a light terminal).
	solidLine := func(text string, visLen int) string {
		pad := width - visLen
		if pad < 0 {
			pad = 0
		}
		return reapplyBackground(text+strings.Repeat(" ", pad), msgBG) + "\x1b[0m"
	}

	if msg.IsBashCommand && msg.Content != "" {
		if strings.HasPrefix(msg.Content, "!") {
			command := msg.Content[1:]
			bashFG := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Bold(true)
			styled := bashFG.Render("! " + command)
			msgLines = append(msgLines, solidLine(styled, 2+len([]rune(command))))
		}
	} else {
		// Regular user message — wrapText returns plain text, no ANSI codes.
		wrapped := rawWrap(msg.Content, width-4)

		// Edit target header
		if isEditTarget {
			current, total := ctx.GetEditPosition()
			toRemove := ctx.CountMessagesToRemove(totalMessages)
			positionText := tr("classic.chat.editing_position", current, total)
			if toRemove > 1 {
				positionText += tr("classic.chat.will_remove", toRemove-1)
			}
			editFG := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Warning)).Bold(true)
			styled := editFG.Render(positionText)
			msgLines = append(msgLines, solidLine(styled, len([]rune(positionText))))
		}

		// Content lines: "  text" padded to full width with msgBG background.
		for _, line := range wrapped {
			styled := fgStyle.Render("  " + line)
			msgLines = append(msgLines, solidLine(styled, 2+len([]rune(line))))
		}

		if len(wrapped) == 0 {
			msgLines = append(msgLines, solidLine("", 0))
		}

		// Visual cut line after edit target
		if isEditTarget {
			cutStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Error)).
				Bold(true)
			cutLine := strings.Repeat("─", width-4)
			msgLines = append(msgLines, reapplyBackground("", th.BG))
			msgLines = append(msgLines, reapplyBackground(cutStyle.Render("  ✂ "+cutLine+tr("classic.chat.removed_below")), th.BG))
			msgLines = append(msgLines, reapplyBackground("", th.BG))
		}

		// Attachments
		for _, att := range msg.Attachments {
			sizeKB := att.Size / 1024
			icon := "📎"
			if strings.HasPrefix(att.MimeType, "image/") {
				icon = "🖼️"
			}
			attText := fmt.Sprintf("%s %s (%d KB)", icon, att.FileName, sizeKB)
			attFG := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted))
			styled := attFG.Render("  " + attText)
			msgLines = append(msgLines, solidLine(styled, 2+len([]rune(attText))))
		}

		// User-pasted images
		if len(msg.Images) > 0 {
			imgHdrFG := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted))
			msgLines = append(msgLines, solidLine(imgHdrFG.Render(tr("classic.image.attached")), 20))
			for _, img := range msg.Images {
				imageBlock := a.renderInlineImage(img, width-4, msgBG)
				lineBase := len(msgLines)
				for _, anchor := range imageBlock.Images {
					anchor.Line += lineBase
					imageAnchors = append(imageAnchors, anchor)
				}
				for _, imgLine := range imageBlock.Lines {
					msgLines = append(msgLines, reapplyBackground("  "+imgLine, msgBG))
				}
			}
		}
	}

	return renderedImageBlock{Lines: msgLines, Images: imageAnchors}
}
