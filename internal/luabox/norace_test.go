//go:build !race

package luabox

// raceSlowdown stretches wall-clock bounds of tests under the race detector;
// without it, bounds stay as written.
const raceSlowdown = 1
