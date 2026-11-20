package agentcli

import (
	"regexp"
	"strings"
	"unicode"
)

var imagePath = regexp.MustCompile(`(?i)^(?:/|[a-z]:[\\/]|\\\\)[^\r\n]*\.(png|jpe?g|gif|webp)$`)
var imageMarker = regexp.MustCompile(`\[Image#[1-9][0-9]*\]`)

// PasteReady returns an optional screen check for an asynchronous image paste.
// Claude reads an image path in the background and inserts its chip at the
// current cursor. Sending more text before the chip appears moves that cursor.
// Other providers do not use this Claude-specific acknowledgement.
func PasteReady(provider Provider, pasted, before string) func(string) bool {
	if provider != Claude {
		return nil
	}
	pasted = strings.TrimSuffix(strings.TrimPrefix(pasted, "\x1b[200~"), "\x1b[201~")
	path := pasted
	if len(path) >= 2 && (path[0] == '\'' || path[0] == '"') && path[len(path)-1] == path[0] {
		path = path[1 : len(path)-1]
	}
	if !imagePath.MatchString(path) {
		return nil
	}
	before = compactScreen(claudeComposer(before))
	markers := markerCounts(before)
	typed := compactScreen(pasted)
	return func(screen string) bool {
		screen = compactScreen(claudeComposer(screen))
		for marker, count := range markerCounts(screen) {
			if count > markers[marker] {
				return true
			}
		}
		// If the file cannot be read as an image, Claude finishes its read by
		// inserting the original path as text. That is confirmation too.
		return strings.Count(screen, typed) > strings.Count(before, typed)
	}
}

// Earlier submitted prompts can contain the same image numbers, and can scroll
// away during a redraw. Only the last prompt is the active input being filled.
func claudeComposer(screen string) string {
	rows := strings.Split(screen, "\n")
	for i := len(rows) - 1; i >= 0; i-- {
		line := strings.TrimSpace(rows[i])
		dollarPrompt := strings.HasPrefix(line, "$") && (len(line) == 1 || unicode.IsSpace([]rune(line)[1]))
		if strings.HasPrefix(line, "❯") || dollarPrompt {
			return strings.Join(rows[i:], "\n")
		}
	}
	return screen
}

func markerCounts(screen string) map[string]int {
	counts := make(map[string]int)
	for _, marker := range imageMarker.FindAllString(screen, -1) {
		counts[marker]++
	}
	return counts
}

// Terminal rows can wrap a chip or a path; padding and line breaks are layout.
func compactScreen(screen string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, screen)
}
