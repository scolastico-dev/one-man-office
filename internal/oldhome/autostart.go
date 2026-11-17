package oldhome

import (
	"crypto/sha256"
	"fmt"
)

// entryName is the name the old omo gave the dashboard's login autostart
// entry: derived from the path of its settings file (company/autostart.json
// in the home).
func entryName(config string) string {
	sum := sha256.Sum256([]byte(config))
	return fmt.Sprintf("dev.scolastico.omo-company-%x", sum[:8])
}
