//go:build race

package luabox

// raceSlowdown stretches wall-clock bounds of tests: the race detector makes
// the interpreter several times slower.
const raceSlowdown = 4
