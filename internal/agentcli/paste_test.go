package agentcli

import "testing"

func TestClaudeImagePasteWaitsForANewImage(t *testing.T) {
	before := "previous message [Image #1]\n❯ right: "
	ready := PasteReady(Claude, "\x1b[200~'/images/shot one.png'\x1b[201~", before)
	if ready == nil {
		t.Fatal("image paste has no readiness check")
	}
	for _, screen := range []string{before, before + "\nPasting…", "previous message [Image #1]\n❯ right: [Image #1]"} {
		want := screen == "previous message [Image #1]\n❯ right: [Image #1]"
		if got := ready(screen); got != want {
			t.Errorf("ready(%q) = %t, want %t", screen, got, want)
		}
	}
	if !ready("previous message [Image #1]\n❯ right: [Image\n #2]") {
		t.Fatal("wrapped image marker was not recognized")
	}
}

func TestImagePasteReadFailureCanFinishAsText(t *testing.T) {
	ready := PasteReady(Claude, "/missing.png", "❯ right: ")
	if ready == nil || !ready("❯ right: /missing.png") {
		t.Fatal("a path pasted as text after a failed image read never settles")
	}
}

func TestImageAcknowledgementIgnoresTheTranscriptAboveTheComposer(t *testing.T) {
	before := "❯ previous message [Image #1]\nanswer\n❯ right: "
	ready := PasteReady(Claude, "/one.png", before)
	if ready == nil {
		t.Fatal("image paste has no readiness check")
	}
	if !ready("answer\n❯ right: [Image #1]") {
		t.Fatal("a previous image scrolling away hid the new image's acknowledgement")
	}
	if ready("❯ previous message [Image #1] [Image #2]\nanswer\n❯ right: ") {
		t.Fatal("an image in the transcript acknowledged the unfinished paste")
	}
	// Screen-reader/simple mode uses a dollar prompt instead of the chevron.
	ready = PasteReady(Claude, "/one.png", "$ previous [Image #1]\nanswer\n$ right: ")
	if !ready("answer\n$ right: [Image #1]") {
		t.Fatal("the simple-mode image acknowledgement was lost")
	}
	ready = PasteReady(Claude, "/one.png", "$\u00a0previous [Image #1]\nanswer\n$\u00a0right: ")
	if !ready("answer\n$\u00a0right: [Image #1]") {
		t.Fatal("the screen-reader prompt's non-breaking space hid the acknowledgement")
	}
}

func TestPasteReadinessIsOnlyForClaudeImagePaths(t *testing.T) {
	for _, provider := range []Provider{Claude, Codex, Agy, Gemini, ""} {
		for _, paste := range []string{"text", "\r- left: ", "/images/a.png", `"C:\images\a.JPG"`} {
			want := provider == Claude && (paste == "/images/a.png" || paste == `"C:\images\a.JPG"`)
			if got := PasteReady(provider, paste, ""); (got != nil) != want {
				t.Errorf("readiness for %s, %q: got %t, want %t", provider, paste, got != nil, want)
			}
		}
	}
}
