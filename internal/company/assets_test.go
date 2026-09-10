package company

import (
	"bytes"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDashboardBrandUsesTransparentLogoAndRoundedFavicon(t *testing.T) {
	index := embeddedDashboardAsset(t, "index.html")
	if !strings.Contains(index, `<img class="brand-mark" src="/assets/logo-transparent.png"`) {
		t.Fatal("brand must use the transparent logo artwork")
	}
	if strings.Contains(index, `<img class="brand-mark" src="/assets/logo.jpg"`) {
		t.Fatal("brand must not use the square logo artwork")
	}
	if !strings.Contains(index, `<link rel="icon" href="/assets/favicon.png" type="image/png">`) {
		t.Fatal("dashboard must declare its rounded favicon")
	}
}

func TestDashboardServesRoundedFavicon(t *testing.T) {
	_, server := testServer(t)
	resp, err := server.Client().Get(server.URL + "/assets/favicon.png")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("favicon response: HTTP %d, content type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	image, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode favicon: %v", err)
	}
	if _, _, _, alpha := image.At(0, 0).RGBA(); alpha != 0 {
		t.Fatalf("favicon top-left corner alpha = %d, want transparent", alpha)
	}
	if _, _, _, alpha := image.At(image.Bounds().Dx()/2, image.Bounds().Dy()/2).RGBA(); alpha == 0 {
		t.Fatal("favicon center must retain logo artwork")
	}
}

func TestDashboardBrandAndAddProjectHaveAccessibleInteractiveStyles(t *testing.T) {
	css := embeddedDashboardAsset(t, "app.css")
	if !strings.Contains(css, ".brand-mark { display: block; width: 36px; height: 36px; flex-shrink: 0; object-fit: contain; transition: filter 140ms ease; }") {
		t.Fatal("brand logo must transition its accent filter")
	}
	if !strings.Contains(css, ".brand:hover .brand-mark, .brand:focus-visible .brand-mark") {
		t.Fatal("brand logo must receive an accent effect on hover and keyboard focus")
	}
	if strings.Contains(css, "#add-project { background:") || strings.Contains(css, "#add-project { border-color:") {
		t.Fatal("Add project must remain neutral at rest")
	}
	if !strings.Contains(css, "#add-project:hover:not(:disabled), #add-project:focus-visible") {
		t.Fatal("Add project must receive its accent state only on interaction")
	}
	if !strings.Contains(css, ".brand:focus-visible { outline: 2px solid var(--accent); outline-offset: 3px; }") {
		t.Fatal("brand link must expose a visible keyboard focus state")
	}
	if !strings.Contains(css, "@media (prefers-reduced-motion: reduce)") || !strings.Contains(css, ".brand-mark { transition: none; }") {
		t.Fatal("reduced motion must remove the brand transition")
	}
}

func TestDashboardSidebarTerminalAndStatusShareTheContentBottomEdge(t *testing.T) {
	css := embeddedDashboardAsset(t, "app.css")
	if !strings.Contains(css, "#supervisor-sidebar { width: 290px; flex-shrink: 0; display: flex; flex-direction: column; gap: 20px; overflow-y: auto; padding: 0 3px; scrollbar-width: thin;") {
		t.Fatal("desktop sidebar must not consume extra bottom padding")
	}
	if !strings.Contains(css, ".terminal-list { flex: 1; min-height: 140px; }") || !strings.Contains(css, "overflow-y: auto; padding: 0 3px;") {
		t.Fatal("terminal list must retain flexible internal scrolling")
	}
	if !strings.Contains(css, "#supervisor-sidebar { width: 100%; flex-shrink: 0; display: grid; grid-template-columns: 1fr 1fr; gap: 18px 10px; overflow: visible; }") {
		t.Fatal("narrow sidebar must retain its stacked navigation layout")
	}
	if !strings.Contains(css, "#supervisor-main { flex: 1 0 480px; }") {
		t.Fatal("narrow main must retain its terminal layout sizing")
	}
}

func embeddedDashboardAsset(t *testing.T, name string) string {
	t.Helper()
	data, err := assets.ReadFile("assets/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
