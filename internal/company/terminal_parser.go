package company

// terminalVTState follows the Paul Williams ANSI parser state names. The
// parser deliberately keeps control-sequence recognition independent from
// terminal mode policy; mode changes happen only through dispatch.
type terminalVTState uint8

const (
	terminalVTGround terminalVTState = iota
	terminalVTEscape
	terminalVTEscapeIntermediate
	terminalVTCsiEntry
	terminalVTCsiParam
	terminalVTCsiIntermediate
	terminalVTCsiIgnore
	terminalVTDcsEntry
	terminalVTDcsParam
	terminalVTDcsIntermediate
	terminalVTDcsPassthrough
	terminalVTDcsIgnore
	terminalVTOscString
	terminalVTSosPmApcString
)

type terminalDispatchKind uint8

const (
	terminalDispatchPrivateMode terminalDispatchKind = iota + 1
	terminalDispatchRIS
	terminalDispatchDECSTR
)

type terminalDispatch struct {
	kind       terminalDispatchKind
	set        bool
	params     [16]int
	paramCount int
}

type terminalParserSnapshot struct {
	state          terminalVTState
	utf8Remaining  uint8
	csiPrivate     bool
	csiDECSTR      bool
	csiInvalid     bool
	csiParams      [16]int
	csiParamCount  uint8
	csiCurrent     int
	csiHasCurrent  bool
	csiLastWasSemi bool
}

type terminalParser struct {
	terminalParserSnapshot
}

func (p terminalParser) snapshot() terminalParserSnapshot {
	return p.terminalParserSnapshot
}

func (p *terminalParser) restore(snapshot terminalParserSnapshot) {
	p.terminalParserSnapshot = snapshot
}

func (p terminalParser) safeBoundary() bool {
	return p.state == terminalVTGround && p.utf8Remaining == 0
}

func (p *terminalParser) feed(data []byte, dispatch func(terminalDispatch)) {
	for _, b := range data {
		p.feedByte(b, dispatch)
	}
}

func (p *terminalParser) feedByte(b byte, dispatch func(terminalDispatch)) {
	if p.state == terminalVTGround && p.utf8Remaining > 0 {
		if b >= 0x80 && b <= 0xbf {
			p.utf8Remaining--
			return
		}
		p.utf8Remaining = 0
	}

	// Paul Williams' anywhere transitions precede the state-specific table.
	if b == 0x1b {
		p.state = terminalVTEscape
		p.clearCSI()
		return
	}
	if b == 0x18 || b == 0x1a {
		p.state = terminalVTGround
		p.utf8Remaining = 0
		p.clearCSI()
		return
	}
	if b == 0x9c && (p.state == terminalVTDcsPassthrough || p.state == terminalVTDcsIgnore || p.state == terminalVTOscString || p.state == terminalVTSosPmApcString) {
		p.state = terminalVTGround
		p.clearCSI()
		return
	}

	if p.state == terminalVTGround {
		if width := terminalUTF8Width(b); width > 1 {
			p.utf8Remaining = uint8(width - 1)
			return
		}
	}

	switch p.state {
	case terminalVTGround:
		p.feedGround(b)
	case terminalVTEscape:
		p.feedEscape(b, dispatch)
	case terminalVTEscapeIntermediate:
		p.feedEscapeIntermediate(b)
	case terminalVTCsiEntry:
		p.feedCSIEntry(b)
	case terminalVTCsiParam:
		p.feedCSIParam(b, dispatch)
	case terminalVTCsiIntermediate:
		p.feedCSIIntermediate(b, dispatch)
	case terminalVTCsiIgnore:
		if b >= 0x40 && b <= 0x7e {
			p.state = terminalVTGround
		}
	case terminalVTDcsEntry:
		p.feedDCSEntry(b)
	case terminalVTDcsParam:
		p.feedDCSParam(b)
	case terminalVTDcsIntermediate:
		p.feedDCSIntermediate(b)
	case terminalVTDcsPassthrough:
		// Data is ignored until ST; anywhere ESC/CAN/SUB handled above.
	case terminalVTDcsIgnore:
		if b >= 0x40 && b <= 0x7e {
			p.state = terminalVTGround
		}
	case terminalVTOscString, terminalVTSosPmApcString:
		if b == 0x07 && p.state == terminalVTOscString {
			p.state = terminalVTGround
		} else if b == 0x9c {
			p.state = terminalVTGround
		}
	}
}

func (p *terminalParser) feedGround(b byte) {
	switch b {
	case 0x90:
		p.state = terminalVTDcsEntry
	case 0x98, 0x9e, 0x9f:
		p.state = terminalVTSosPmApcString
	case 0x9b:
		p.beginCSI()
	case 0x9d:
		p.state = terminalVTOscString
	case 0x9c:
		// ST in ground is ignored.
	case 0x00, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
		0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x19,
		0x1c, 0x1d, 0x1e, 0x1f:
		// C0 controls execute without leaving ground.
	default:
		// Printable bytes are intentionally ignored by the mode parser.
	}
}

func (p *terminalParser) feedEscape(b byte, dispatch func(terminalDispatch)) {
	switch b {
	case '[':
		p.beginCSI()
	case 'P':
		p.state = terminalVTDcsEntry
	case 'X', '^', '_':
		p.state = terminalVTSosPmApcString
	case ']':
		p.state = terminalVTOscString
	case 'c':
		p.state = terminalVTGround
		p.clearCSI()
		if dispatch != nil {
			dispatch(terminalDispatch{kind: terminalDispatchRIS})
		}
	default:
		if b >= 0x20 && b <= 0x2f {
			p.state = terminalVTEscapeIntermediate
		} else if b >= 0x30 && b <= 0x7e {
			p.state = terminalVTGround
		}
	}
}

func (p *terminalParser) feedEscapeIntermediate(b byte) {
	if b >= 0x20 && b <= 0x2f {
		return
	}
	if b >= 0x30 && b <= 0x7e {
		p.state = terminalVTGround
	}
}

func (p *terminalParser) beginCSI() {
	p.clearCSI()
	p.state = terminalVTCsiEntry
}

func (p *terminalParser) clearCSI() {
	p.csiPrivate = false
	p.csiDECSTR = false
	p.csiInvalid = false
	p.csiParams = [16]int{}
	p.csiParamCount = 0
	p.csiCurrent = 0
	p.csiHasCurrent = false
	p.csiLastWasSemi = false
}

func (p *terminalParser) feedCSIEntry(b byte) {
	switch {
	case b >= 0x30 && b <= 0x3f:
		if b == '?' {
			p.csiPrivate = true
			p.state = terminalVTCsiParam
		} else if b >= '0' && b <= '9' {
			p.csiCurrent = int(b - '0')
			p.csiHasCurrent = true
			p.state = terminalVTCsiParam
		} else {
			p.state = terminalVTCsiIgnore
		}
	case b >= 0x20 && b <= 0x2f:
		p.csiDECSTR = b == '!'
		p.state = terminalVTCsiIntermediate
	case b >= 0x40 && b <= 0x7e:
		p.state = terminalVTGround
	default:
		// C0 execute leaves the entry state intact.
	}
}

func (p *terminalParser) feedCSIParam(b byte, dispatch func(terminalDispatch)) {
	switch {
	case b >= '0' && b <= '9':
		if p.csiCurrent > 9999999 {
			p.csiInvalid = true
		} else {
			p.csiCurrent = p.csiCurrent*10 + int(b-'0')
			p.csiHasCurrent = true
			p.csiLastWasSemi = false
		}
	case b == ';':
		p.storeCSIParam()
		p.csiLastWasSemi = true
	case b >= 0x40 && b <= 0x7e:
		p.dispatchCSI(b, dispatch)
	case b >= 0x20 && b <= 0x2f:
		p.state = terminalVTCsiIntermediate
	default:
		p.csiInvalid = true
	}
}

func (p *terminalParser) storeCSIParam() {
	if !p.csiHasCurrent || p.csiParamCount >= uint8(len(p.csiParams)) {
		p.csiInvalid = true
	} else {
		p.csiParams[p.csiParamCount] = p.csiCurrent
		p.csiParamCount++
		p.csiCurrent = 0
		p.csiHasCurrent = false
	}
}

func (p *terminalParser) dispatchCSI(final byte, dispatch func(terminalDispatch)) {
	if p.csiHasCurrent {
		p.storeCSIParam()
	} else if p.csiLastWasSemi || p.csiParamCount == 0 {
		p.csiInvalid = true
	}
	if dispatch != nil && !p.csiInvalid && p.csiPrivate && (final == 'h' || final == 'l') {
		dispatch(terminalDispatch{kind: terminalDispatchPrivateMode, set: final == 'h', params: p.csiParams, paramCount: int(p.csiParamCount)})
	}
	p.state = terminalVTGround
	p.clearCSI()
}

func (p *terminalParser) feedCSIIntermediate(b byte, dispatch func(terminalDispatch)) {
	if b >= 0x20 && b <= 0x2f {
		return
	}
	if b >= 0x40 && b <= 0x7e {
		if dispatch != nil && p.csiDECSTR && b == 'p' {
			dispatch(terminalDispatch{kind: terminalDispatchDECSTR})
		}
		p.state = terminalVTGround
		p.clearCSI()
	} else {
		p.state = terminalVTCsiIgnore
	}
}

func (p *terminalParser) feedDCSEntry(b byte) {
	switch {
	case b >= 0x30 && b <= 0x3f:
		p.state = terminalVTDcsParam
	case b >= 0x20 && b <= 0x2f:
		p.state = terminalVTDcsIntermediate
	case b >= 0x40 && b <= 0x7e:
		p.state = terminalVTDcsPassthrough
	default:
		p.state = terminalVTDcsIgnore
	}
}

func (p *terminalParser) feedDCSParam(b byte) {
	switch {
	case b >= 0x30 && b <= 0x3f:
		return
	case b >= 0x20 && b <= 0x2f:
		p.state = terminalVTDcsIntermediate
	case b >= 0x40 && b <= 0x7e:
		p.state = terminalVTDcsPassthrough
	default:
		p.state = terminalVTDcsIgnore
	}
}

func (p *terminalParser) feedDCSIntermediate(b byte) {
	switch {
	case b >= 0x20 && b <= 0x2f:
		return
	case b >= 0x40 && b <= 0x7e:
		p.state = terminalVTDcsPassthrough
	default:
		p.state = terminalVTDcsIgnore
	}
}

func terminalUTF8Width(lead byte) int {
	switch {
	case lead >= 0xc2 && lead <= 0xdf:
		return 2
	case lead >= 0xe0 && lead <= 0xef:
		return 3
	case lead >= 0xf0 && lead <= 0xf4:
		return 4
	default:
		return 1
	}
}
