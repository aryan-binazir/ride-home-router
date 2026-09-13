package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"ride-home-router/internal/models"
	"strings"
	"testing"
	"time"
)

// Render the real templates and run the real CSS and planner swap listener.
// No server, Google calls, or database is needed for these layout regressions.
func TestPlannerBrowserContainment(t *testing.T) {
	browser := os.Getenv("BROWSER_TEST_BINARY")
	if browser == "" {
		t.Skip("BROWSER_TEST_BINARY is not set")
	}
	renderer := loadEmbeddedTemplates(t)
	view := IndexPageView{ActivePage: ActivePageHome, ActivityLocations: []models.ActivityLocation{{ID: 1, Name: "Youth Center", Address: "1 Main St"}}}
	for i := range 1000 {
		view.Participants = append(view.Participants, models.Participant{ID: int64(i + 1), Name: fmt.Sprintf("Rider %d", i), Address: "1 Main St"})
	}
	for i := range 220 {
		view.Drivers = append(view.Drivers, models.Driver{ID: int64(i + 1), Name: fmt.Sprintf("Driver %d", i), Address: "2 Main St", VehicleCapacity: 4})
	}
	for i, name := range []string{"Youth club - 40 riders", "Friday program - 80 riders", "Community day - 300 riders", "Triangle gathering - 500 riders", "Durham neighbors", "Chapel Hill neighbors", "Raleigh neighbors"} {
		view.Labels = append(view.Labels, models.Label{ID: int64(i + 1), Name: name})
	}
	var page bytes.Buffer
	if err := renderer.Render(&page, "index.html", view); err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		//nolint:gosec // Names are fixed test assets below.
		b, e := os.ReadFile(filepath.Join("../../web/static", name))
		if e != nil {
			t.Fatal(e)
		}
		return string(b)
	}
	var routePage bytes.Buffer
	if err := renderer.Render(&routePage, "route_results", RouteResultsView{
		Routes:           []models.CalculatedRoute{{Driver: &view.Drivers[0], EffectiveCapacity: 4, Stops: []models.RouteStop{{Participant: &view.Participants[0]}}}},
		Timings:          []RouteTiming{{Status: "missing_key", Message: "Timings need the Google Maps key. Ask an administrator."}},
		ActivityLocation: &view.ActivityLocations[0], RouteTime: "18:30", SessionID: "test", Mode: "dropoff",
	}); err != nil {
		t.Fatal(err)
	}
	routeJSON, err := json.Marshal(routePage.String())
	if err != nil {
		t.Fatal(err)
	}
	html := page.String()
	html = strings.ReplaceAll(html, `<link rel="stylesheet" href="/static/css/style.css">`, "<style>"+read("css/style.css")+"</style>")
	for _, name := range []string{"event-planner.js", "ui.js"} {
		html = strings.ReplaceAll(html, `<script src="/static/js/`+name+`" defer></script>`, "<script>"+read("js/"+name)+"</script>")
	}
	html = strings.ReplaceAll(html, `<script src="/static/js/htmx.min.js"></script>`, "<script>"+read("js/htmx.min.js")+"</script>")
	html = strings.ReplaceAll(html, `<script src="/static/js/auth.js?v=20260912-theme" defer></script>`, "")
	// These are outer window sizes; Chromium reserves space for browser controls.
	// Exact viewport sizes are also exercised against the running app.
	for _, size := range [][2]int{{1000, 850}, {1440, 1100}, {2000, 1500}, {390, 1044}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			dir, err := os.MkdirTemp("/tmp", "rhr-layout-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			script := `<pre id="qa-result" style="position:fixed;display:none"></pre><script>
window.addEventListener('load',()=>{
 const errors=[]; const check=(ok,msg)=>{if(!ok)errors.push(msg)};
 const wide=innerWidth>=821;
 const mobileLink=document.querySelector('.desktop-mode-footer a');
 const desktopPointer=matchMedia('(min-width: 821px) and (hover: hover) and (pointer: fine)').matches;
 check((getComputedStyle(mobileLink).display!=='none')===!desktopPointer,'mobile link visibility');

 if(innerWidth===1440) {const d=document.querySelector('#drivers-search').getBoundingClientRect();check(d.top>=0&&d.bottom<document.querySelector('.plan-pane-scroll').getBoundingClientRect().bottom,'drivers search buried '+JSON.stringify({h:innerHeight,d:d.bottom,pane:document.querySelector('.plan-pane-scroll').getBoundingClientRect().bottom}));}
 const target=document.querySelector('#results-section');
 target.innerHTML=ROUTE_HTML;
 target.querySelector('.results-body').insertAdjacentHTML('afterbegin','<div style="height:20000px">Long route list</div>');
 const date=target.querySelector('[name="event_date"]'), notes=target.querySelector('[name="notes"]'), save=target.querySelector('button[type="submit"]');
 check(date.labels.length===1&&notes.labels.length===1,'save labels must identify their fields');
 check(Math.abs(date.getBoundingClientRect().height-notes.getBoundingClientRect().height)<1,'notes height differs from date');
 check(Math.abs(date.getBoundingClientRect().height-save.getBoundingClientRect().height)<1,'save height differs from date');
 document.body.dispatchEvent(new CustomEvent('htmx:afterSwap',{detail:{target}}));
 setTimeout(()=>{
  if(wide)check(scrollY===0,'document scrolled after calculate: '+scrollY);
  date.focus();
  document.querySelector('#save-event-panel').scrollIntoView();
  if(wide) {check(scrollY===0,'document scrolled on focus/save: '+scrollY);check(document.querySelector('.results-scroll').scrollTop>0,'results did not scroll');check(document.querySelector('.appbar').getBoundingClientRect().top===0,'header offscreen');}
  document.querySelector('#qa-result').textContent=errors.length?errors.join('; '):'PASS';
 },300);
});</script>`
			script = strings.Replace(script, "ROUTE_HTML", string(routeJSON), 1)
			file := filepath.Join(dir, "fixture.html")
			if err := os.WriteFile(file, []byte(html[:strings.LastIndex(html, "</body>")]+script+html[strings.LastIndex(html, "</body>"):]), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()
			//nolint:gosec // Explicit test-only browser executable, never request input.
			cmd := exec.CommandContext(ctx, browser, "--headless", "--no-sandbox", "--disable-gpu", "--no-first-run", "--hide-scrollbars", fmt.Sprintf("--window-size=%d,%d", size[0], size[1]), "--user-data-dir="+filepath.Join(dir, "profile"), "--dump-dom", "--virtual-time-budget=1500", "file://"+file)
			cmd.WaitDelay = 2 * time.Second
			out, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(out, []byte(`id="qa-result" style="position:fixed;display:none">PASS</pre>`)) {
				s := string(out)
				start := strings.Index(s, `<pre id="qa-result"`)
				if start >= 0 {
					s = s[start:]
					if end := strings.Index(s, "</pre>"); end >= 0 {
						s = s[:end]
					}
				}
				t.Fatalf("layout regression: %s", s)
			}
		})
	}
}
