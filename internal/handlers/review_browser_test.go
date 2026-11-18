package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"ride-home-router/internal/models"
	"ride-home-router/web"
	"strings"
	"testing"
)

func TestAddingSecondDriverBrowserPreservesMeasuredTimings(t *testing.T) {
	browser := os.Getenv("BROWSER_TEST_BINARY")
	if browser == "" {
		t.Skip("BROWSER_TEST_BINARY not set")
	}
	h, snapshot := oneRouteWithUnusedDriver(t)
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("../../web/static"))))
	mux.HandleFunc("/api/v1/routes/edit/add-driver", h.HandleAddDriver)
	mux.HandleFunc("/measurement-count", func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, h.Measurer.(*stubMeasurer).count()) })
	mux.HandleFunc("/api/v1/routes/calculate", func(w http.ResponseWriter, r *http.Request) {
		h.renderTemplate(w, "route_results", h.buildTimedRouteResultsView(snapshot, h.routeTimings(r.Context(), snapshot, []int{0})))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		location := *snapshot.ActivityLocation
		location.ID = 1
		view := IndexPageView{ActivityLocations: []models.ActivityLocation{location}, SelectedLocation: &location, Drivers: append([]models.Driver{*snapshot.Routes[0].Driver}, snapshot.UnusedDrivers...), Participants: []models.Participant{*snapshot.Routes[0].Stops[0].Participant}}
		recorder := httptest.NewRecorder()
		h.renderTemplate(recorder, "index.html", view)
		html := strings.ReplaceAll(recorder.Body.String(), `<script src="`+web.AssetURL("js/auth.js")+`" defer></script>`, "")
		script := `<script>addEventListener('load',async()=>{
 const result={};const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
 try{
  selectAllParticipants();selectAllDrivers();document.getElementById('calculate-btn').click();
  for(let i=0;i<200&&!document.querySelector('.route-card[data-timings="measured"]');i++)await delay(20);
  const before=document.querySelector('.route-card[data-route-index="0"] .route-timings').textContent;
  result.before=document.querySelector('.route-card').dataset.timings;
  result.callsBefore=Number(await(await fetch('/measurement-count')).text());
  result.added=await addUnusedDriver(2);
  result.callsAfter=Number(await(await fetch('/measurement-count')).text());
  const card=document.querySelector('.route-card[data-route-index="0"]');
  result.after=card.dataset.timings;result.same=before===card.querySelector('.route-timings').textContent;
  result.cards=document.querySelectorAll('.route-card').length;result.move=!!card.querySelector('.stop-actions button');
 }catch(error){result.error=String(error)}
 document.body.innerHTML='<pre id="browser-result"></pre>';document.getElementById('browser-result').textContent=JSON.stringify(result);
 });</script>`
		_, _ = fmt.Fprint(w, strings.Replace(html, "</body>", script+"</body>", 1))
	})
	raw := runRouteBrowser(t, browser, mux)
	var result struct {
		Before, After, Error    string
		Added, Same, Move       bool
		Cards                   int
		CallsBefore, CallsAfter int
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || result.Before != "measured" || result.After != "measured" || !result.Added || !result.Same || !result.Move || result.Cards != 2 {
		t.Fatalf("timing preservation: %s", raw)
	}
	if result.CallsBefore == 0 || result.CallsAfter != result.CallsBefore {
		t.Fatalf("adding an empty driver must not remeasure the unchanged route: %s", raw)
	}
}
