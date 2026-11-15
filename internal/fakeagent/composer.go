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
)

// composerSettings reads EnvSwallowEnters and EnvDeafFor; ok is false
// when neither is set.
func composerSettings() (swallow int, deaf time.Duration, ok bool) {
	if v := strings.TrimSpace(os.Getenv(EnvSwallowEnters)); v != "" {
		swallow, _ = strconv.Atoi(v)
		ok = true
	}
	if v := strings.TrimSpace(os.Getenv(EnvDeafFor)); v != "" {
		deaf, _ = time.ParseDuration(v)
		ok = true
	}
	return max(swallow, 0), max(deaf, 0), ok
}

// composer turns raw terminal input into submitted lines the way a
// full-screen CLI does: typed characters are echoed into an input line that
// stays put, Enter submits it (a new line follows), Backspace deletes. The
// first swallow Enter presses with text in the line do nothing, and input
// arriving before deafUntil is dropped. Submitted lines come out of the
// returned reader, one per line.
func composer(in io.Reader, echo io.Writer, swallow int, deafUntil time.Time) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		var line []byte
		buf := make([]byte, 256)
		for {
			n, err := in.Read(buf)
			if n > 0 && time.Now().Before(deafUntil) {
				n = 0
			}
			for _, b := range buf[:n] {
				switch {
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
