package company

type terminalReplayChunk struct {
	data   []byte
	before terminalParserSnapshot
}

func safeReplayTail(data []byte, limit int) []byte {
	if limit <= 0 || len(data) == 0 {
		return nil
	}
	return terminalReplayTail([]terminalReplayChunk{{data: data}}, len(data), limit)
}

func terminalReplayTail(chunks []terminalReplayChunk, total, limit int) []byte {
	if total == 0 || limit <= 0 || len(chunks) == 0 {
		return nil
	}
	cut, ok := findTerminalReplayCut(chunks, total, limit)
	if !ok {
		return nil
	}
	var replay []byte
	replay = append(replay, chunks[cut.chunk].data[cut.offset:]...)
	for _, chunk := range chunks[cut.chunk+1:] {
		replay = append(replay, chunk.data...)
	}
	return replay
}

func trimTerminalReplay(chunks []terminalReplayChunk, total, limit int) ([]terminalReplayChunk, int) {
	for len(chunks) > 1 && total > limit {
		total -= len(chunks[0].data)
		chunks = chunks[1:]
	}
	if len(chunks) == 1 && total > limit {
		cut, ok := findTerminalReplayCut(chunks, total, limit)
		if !ok {
			return nil, 0
		}
		chunk := chunks[0]
		chunk.data = append([]byte(nil), chunk.data[cut.offset:]...)
		chunk.before = cut.before
		chunks[0] = chunk
		total = len(chunk.data)
	}
	return chunks, total
}

type terminalReplayCutResult struct {
	chunk  int
	offset int
	before terminalParserSnapshot
}

func findTerminalReplayCut(chunks []terminalReplayChunk, total, limit int) (terminalReplayCutResult, bool) {
	target := total - limit
	if target < 0 {
		target = 0
	}
	parser := terminalParser{}
	parser.restore(chunks[0].before)
	offset := 0
	for chunkIndex, chunk := range chunks {
		for byteIndex, b := range chunk.data {
			if offset >= target && (b == 0x1b || (parser.safeBoundary() && !isUTF8Continuation(b))) {
				return terminalReplayCutResult{chunk: chunkIndex, offset: byteIndex, before: parser.snapshot()}, true
			}
			parser.feedByte(b, nil)
			offset++
		}
	}
	if parser.safeBoundary() && offset >= target {
		cutChunk := len(chunks) - 1
		return terminalReplayCutResult{chunk: cutChunk, offset: len(chunks[cutChunk].data), before: parser.snapshot()}, true
	}
	return terminalReplayCutResult{}, false
}

func isUTF8Continuation(b byte) bool {
	return b >= 0x80 && b <= 0xbf
}
