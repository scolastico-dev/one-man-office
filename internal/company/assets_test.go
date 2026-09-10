package company

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"regexp"
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
	if !strings.Contains(index, `<button type="button" class="brand panel"`) {
		t.Fatal("brand must remain a focusable non-navigation control")
	}
	if strings.Contains(index, `<a class="brand panel"`) || strings.Contains(index, `href="/"`) {
		t.Fatal("brand must not reload the dashboard and lose its in-memory capability")
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

func TestDashboardResponsiveGeometryInChrome(t *testing.T) {
	chrome, err := exec.LookPath("google-chrome")
	if err != nil {
		t.Skip("google-chrome is not installed")
	}
	projectHome(t)
	s, err := New(Options{MaxAgents: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ts := httptest.NewUnstartedServer(nil)
	measurePage := dashboardGeometryPage(ts.URL)
	dashboardHandler := s.Handler()
	ts.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/measure.html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, measurePage)
			return
		}
		response := httptest.NewRecorder()
		dashboardHandler.ServeHTTP(response, r)
		if r.URL.Path == "/" {
			response.Header().Del("Content-Security-Policy")
		}
		for key, values := range response.Header() {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(response.Code)
		_, _ = w.Write(response.Body.Bytes())
	})
	s.authority = ts.Listener.Addr().String()
	ts.Start()
	defer ts.Close()

	for _, viewport := range []struct {
		name          string
		width, height int
		wide          bool
	}{
		{name: "wide-tall", width: 1440, height: 900, wide: true},
		{name: "wide-short", width: 1024, height: 650, wide: true},
		{name: "narrow-tall", width: 390, height: 844},
		{name: "narrow-short", width: 390, height: 667},
	} {
		geometry := runDashboardGeometry(t, chrome, ts.URL+"/measure.html", viewport.width, viewport.height)
		t.Logf("%s geometry: projects bottom=%.5f, live terminals bottom=%.5f, footer bottom=%.5f, footer/document gap=%.5f", viewport.name, geometry.ProjectsBottom, geometry.SidebarBottom, geometry.FooterBottom, geometry.FooterPageGap)
		if viewport.wide {
			if delta := math.Abs(geometry.SidebarBottom - geometry.FooterBottom); delta > 0.01 {
				t.Errorf("%s live terminals/footer bottom delta = %.5f, want 0", viewport.name, delta)
			}
			continue
		}
		if delta := math.Abs(geometry.ProjectsBottom - geometry.SidebarBottom); delta > 0.01 {
			t.Errorf("%s sidebar list-row bottom delta = %.5f, want 0", viewport.name, delta)
		}
		if gap := math.Abs(geometry.FooterPageGap); gap > 1 {
			t.Errorf("%s footer/document edge gap = %.5f, want <= 1px rounding", viewport.name, geometry.FooterPageGap)
		}
	}
}

type dashboardGeometry struct {
	ProjectsBottom float64 `json:"projectsBottom"`
	SidebarBottom  float64 `json:"sidebarBottom"`
	FooterBottom   float64 `json:"footerBottom"`
	FooterPageGap  float64 `json:"footerPageGap"`
}

func runDashboardGeometry(t *testing.T, chrome, pageURL string, width, height int) dashboardGeometry {
	t.Helper()
	output, err := exec.Command(chrome,
		"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--user-data-dir="+t.TempDir(), fmt.Sprintf("--window-size=%d,%d", width, height),
		"--virtual-time-budget=2000", "--dump-dom", pageURL,
	).Output()
	if err != nil {
		t.Fatalf("Chrome geometry at %dx%d: %v", width, height, err)
	}
	match := regexp.MustCompile(`<pre id="geometry">(\{[^<]+\})</pre>`).FindSubmatch(output)
	if len(match) != 2 {
		t.Fatalf("Chrome geometry at %dx%d missing result: %s", width, height, output)
	}
	var geometry dashboardGeometry
	if err := json.Unmarshal(match[1], &geometry); err != nil {
		t.Fatalf("decode Chrome geometry at %dx%d: %v", width, height, err)
	}
	return geometry
}

func dashboardGeometryPage(serverURL string) string {
	return fmt.Sprintf(`<!doctype html>
<iframe src="%s/" style="position:fixed;inset:0;width:100%%;height:100%%;border:0"></iframe>
<pre id="geometry"></pre>
<script>
const frame = document.querySelector('iframe');
frame.addEventListener('load', () => setTimeout(() => {
  const doc = frame.contentDocument;
  doc.querySelector('#notice').textContent = '';
  const sections = [...doc.querySelectorAll('#supervisor-sidebar > section')];
  const footer = doc.querySelector('#supervisor-main footer');
  const result = {
    projectsBottom: sections[1].getBoundingClientRect().bottom,
    sidebarBottom: sections[2].getBoundingClientRect().bottom,
    footerBottom: footer.getBoundingClientRect().bottom,
    footerPageGap: Math.max(doc.documentElement.scrollHeight, doc.body.scrollHeight) - footer.getBoundingClientRect().bottom,
  };
  document.querySelector('#geometry').textContent = JSON.stringify(result);
}, 250));
</script>`, serverURL)
}

func embeddedDashboardAsset(t *testing.T, name string) string {
	t.Helper()
	data, err := assets.ReadFile("assets/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
