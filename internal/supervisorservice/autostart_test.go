package supervisorservice

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

func TestLaunchAgentKeepsArgumentsAsXMLStrings(t *testing.T) {
	executable := `/Applications/omo & tools/omo`
	config := `/Users/me/a <b> "c"/autostart.json`
	data, err := launchAgent(executable, config)
	if err != nil {
		t.Fatal(err)
	}
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	var values []string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if start, ok := token.(xml.StartElement); ok && start.Name.Local == "string" {
			var value string
			if err := decoder.DecodeElement(&value, &start); err != nil {
				t.Fatal(err)
			}
			values = append(values, value)
		}
	}
	want := []string{entryName(config), executable, "supervisor", "autostart-run", config}
	if strings.Join(values, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("arguments changed: %#v", values)
	}
	if strings.Contains(string(data), "KeepAlive") {
		t.Fatal("autostart would restart an explicitly stopped supervisor")
	}
}

func TestDesktopEntryEscapesPathsAndDoesNotEmbedSettings(t *testing.T) {
	got, err := desktopEntry("/opt/omo tools/omo", "/tmp/$HOME`whoami`%x\\dir/autostart.json")
	if err != nil {
		t.Fatal(err)
	}
	want := `Exec="/opt/omo tools/omo" supervisor autostart-run "/tmp/\\$HOME\\` + "`" + `whoami\\` + "`" + `%%x\\\\dir/autostart.json"`
	if !strings.Contains(string(got), want) {
		t.Fatalf("wrong Exec quoting:\n%s\nwant: %s", got, want)
	}
	if _, err := desktopEntry("/bin/omo%test", "/tmp/config"); err == nil {
		t.Fatal("accepted an executable path GIO cannot launch")
	}
	if _, err := desktopEntry("/bin/omo\nExec=bad", "/tmp/config"); err == nil {
		t.Fatal("accepted entry injection")
	}
}
