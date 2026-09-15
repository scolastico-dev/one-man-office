package company

import (
	"github.com/charmbracelet/x/ansi"
	ansiparser "github.com/charmbracelet/x/ansi/parser"
)

const terminalReplayChunkLimit = 64 << 10

// terminalReplayChunk is deliberately small metadata: firstSafe is the first
// byte offset after a library-observed safe boundary in this storage chunk,
// or -1 when the chunk has no such boundary. Offset zero means the parser was
// already safe before this chunk. It is recorded during the original parse;
// no parser state is reconstructed while trimming.
type terminalReplayChunk struct {
	data      []byte
	before    terminalParserSnapshot
	firstSafe int
}

func safeReplayTail(data []byte, limit int) []byte {
	if limit <= 0 || len(data) == 0 {
		return nil
	}
	parser := ansi.NewParser()
	target := len(data) - limit
	if target < 0 {
		target = 0
	}
	firstSafe := -1
	if target == 0 {
		firstSafe = 0
	}
	parserSafe := true
	for offset, b := range data {
		action := parser.Advance(b)
		parserSafe = parser.State() == ansiparser.GroundState && action != ansiparser.CollectAction
		if parserSafe && firstSafe < 0 && offset+1 >= target {
			firstSafe = offset + 1
		}
	}
	if firstSafe < 0 {
		return nil
	}
	return append([]byte(nil), data[firstSafe:]...)
}

func terminalReplayTail(chunks []terminalReplayChunk, total, limit int) []byte {
	if total == 0 || limit <= 0 || len(chunks) == 0 {
		return nil
	}
	start, ok := terminalReplayStart(chunks)
	if !ok {
		return nil
	}
	var replay []byte
	replay = append(replay, chunks[start.chunk].data[start.offset:]...)
	for _, chunk := range chunks[start.chunk+1:] {
		replay = append(replay, chunk.data...)
	}
	return replay
}

func trimTerminalReplay(chunks []terminalReplayChunk, total, limit int) ([]terminalReplayChunk, int) {
	if limit <= 0 {
		return nil, 0
	}
	// Drop only whole storage chunks while over the bound. A partial chunk is
	// then cut at its recorded first safe boundary, never inside a sequence.
	for len(chunks) > 0 && total > limit {
		total -= len(chunks[0].data)
		chunks = chunks[1:]
	}
	start, ok := terminalReplayStart(chunks)
	if !ok {
		return nil, 0
	}
	for start.chunk > 0 {
		total -= len(chunks[0].data)
		chunks = chunks[1:]
		start.chunk--
	}
	if start.offset > 0 {
		chunk := chunks[0]
		chunk.data = append([]byte(nil), chunk.data[start.offset:]...)
		chunk.before = terminalParserSnapshot{state: ansiparser.GroundState, safe: true}
		chunk.firstSafe = 0
		chunks[0] = chunk
		total -= start.offset
	}
	if total > limit {
		return nil, 0
	}
	return chunks, total
}

type terminalReplayStartResult struct {
	chunk  int
	offset int
}

func terminalReplayStart(chunks []terminalReplayChunk) (terminalReplayStartResult, bool) {
	for index, chunk := range chunks {
		if chunk.firstSafe >= 0 && chunk.firstSafe <= len(chunk.data) {
			return terminalReplayStartResult{chunk: index, offset: chunk.firstSafe}, true
		}
	}
	return terminalReplayStartResult{}, false
}
