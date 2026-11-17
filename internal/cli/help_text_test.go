package cli

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// A terminal prints help as it is: no Markdown (backticks, bold, headings)
// in the help of any command or flag.
func TestHelpTextHasNoMarkdown(t *testing.T) {
	bold := regexp.MustCompile(`(^|[^*])\*\*[^*\s]`)
	var bad []string
	check := func(where, text string) {
		if strings.Contains(text, "`") || bold.MatchString(text) {
			bad = append(bad, where+": "+strings.Join(strings.Fields(text), " "))
		}
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, "#") {
				bad = append(bad, where+": heading "+line)
			}
		}
	}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		name := c.CommandPath()
		check(name+" Short", c.Short)
		check(name+" Long", c.Long)
		check(name+" Example", c.Example)
		c.Flags().VisitAll(func(f *pflag.Flag) { check(name+" --"+f.Name, f.Usage) })
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(Root("test"))
	if len(bad) > 0 {
		t.Fatalf("Markdown in help texts:\n%s", strings.Join(bad, "\n"))
	}
}
