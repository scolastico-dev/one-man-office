package fakeagent

import (
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Environment variables that make the fake read its terminal like a
// full-screen CLI with an input line instead of line by line.
const (
	// EnvSwallowEnters is a number: that many Enter presses are ignored
	// while text sits in the input line, as a CLI that is still starting
	// or shows a notice does.
	EnvSwallowEnters = "OMO_FAKE_SWALLOW_ENTERS"
	// EnvDeafFor is a duration: input arriving that long after the start
	// is dropped without showing, as during a start-up notice.
	EnvDeafFor = "OMO_FAKE_DEAF_FOR"
	// EnvBracketedPaste, set to 1, makes the fake ask for bracketed paste
	// like the agent CLIs do: text between the paste markers is one input
	// whose line breaks do not submit (they show as "⏎" in the line), and
	// Enter after it does.
	EnvBracketedPaste = "OMO_FAKE_BRACKETED_PASTE"
)

// composerSettings reads EnvSwallowEnters and EnvDeafFor; ok is false
// when neither is set.
func composerSettings() (swallow int, deaf time.Duration, paste, ok bool) {
	if strings.TrimSpace(os.Getenv(EnvBracketedPaste)) == "1" {
		paste, ok = true, true
	}
	if v := strings.TrimSpace(os.Getenv(EnvSwallowEnters)); v != "" {
		swallow, _ = strconv.Atoi(v)
		ok = true
	}
	if v := strings.TrimSpace(os.Getenv(EnvDeafFor)); v != "" {
		deaf, _ = time.ParseDuration(v)
		ok = true
	}
	return max(swallow, 0), max(deaf, 0), paste, ok
}

// composer turns raw terminal input into submitted lines the way a
// full-screen CLI does: typed characters are echoed into an input line that
// stays put, Enter submits it (a new line follows), Backspace deletes. The
// first swallow Enter presses with text in the line do nothing, and input
// arriving before deafUntil is dropped. Submitted lines come out of the
// returned reader, one per line.
func composer(in io.Reader, echo io.Writer, swallow int, deafUntil time.Time) io.Reader {
	return pasteComposer(in, echo, swallow, deafUntil, false)
}

// lineBreakMark stands for a line break inside a pasted message.
const lineBreakMark = "⏎"

// pasteComposer is composer that, with paste set, asks for bracketed paste and
// takes what comes between the markers as part of the line.
func pasteComposer(in io.Reader, echo io.Writer, swallow int, deafUntil time.Time, paste bool) io.Reader {
	pr, pw := io.Pipe()
	if paste {
		_, _ = echo.Write([]byte("\x1b[?2004h"))
	}
	go func() {
		var line []byte
		var esc []byte // an escape sequence being read
		pasting := false
		buf := make([]byte, 256)
		for {
			n, err := in.Read(buf)
			if n > 0 && time.Now().Before(deafUntil) {
				n = 0
			}
			for _, b := range buf[:n] {
				if len(esc) > 0 {
					esc = append(esc, b)
					switch string(esc) {
					case "\x1b[200~":
						pasting, esc = paste, nil
					case "\x1b[201~":
						pasting, esc = false, nil
					default:
						// Anything but a prefix of a paste marker is dropped.
						if !strings.HasPrefix("\x1b[200~", string(esc)) && !strings.HasPrefix("\x1b[201~", string(esc)) {
							esc = nil
						}
					}
					continue
				}
				switch {
				case b == 0x1b:
					esc = []byte{b}
				case (b == '\r' || b == '\n') && pasting:
					line = append(line, lineBreakMark...)
					_, _ = echo.Write([]byte(lineBreakMark))
				case b == '\r' || b == '\n':
					if len(line) == 0 {
						continue
					}
					if swallow > 0 {
						swallow--
						continue
					}
					_, _ = echo.Write([]byte("\r\n"))
					if _, werr := pw.Write(append(line, '\n')); werr != nil {
						return
					}
					line = nil
				case b == 0x7f || b == 0x08:
					if len(line) > 0 {
						_, size := utf8.DecodeLastRune(line)
						line = line[:len(line)-size]
						_, _ = echo.Write([]byte("\b \b"))
					}
				case b < 0x20:
					// Other control keys do nothing here.
				default:
					line = append(line, b)
					_, _ = echo.Write([]byte{b})
				}
			}
			if err != nil {
				pw.CloseWithError(err)
				return
			}
		}
	}()
	return pr
}
