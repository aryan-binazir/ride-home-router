package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"ride-home-router/internal/models"
	"ride-home-router/internal/plandraft"
	"ride-home-router/web"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAddingSecondMobileDriverPreservesTimedCard(t *testing.T) {
	browser := os.Getenv("BROWSER_TEST_BINARY")
	if browser == "" {
		t.Skip("BROWSER_TEST_BINARY not set")
	}
	h, snapshot, cookie := oneRouteWithUnusedDriver(t)
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("../../web/static"))))
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/measurement-count", func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, h.Measurer.(*stubMeasurer).count()) })
	mux.HandleFunc("/m/routes/editor", func(w http.ResponseWriter, r *http.Request) { r.AddCookie(cookie); h.HandleRouteEditor(w, r) })
	mux.HandleFunc("/m/routes/choose", func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(cookie)
		h.HandleMobileRouteEditorAction(w, r)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		recorder := httptest.NewRecorder()
		h.renderMobileRoutesTimed(recorder, r, snapshot, 200, "", "", "", []int{0})
		html := strings.ReplaceAll(recorder.Body.String(), `<script src="`+web.AssetURL("js/auth.js")+`" defer></script>`, "")
		script := `<script>addEventListener('load',async()=>{
 const result={};const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
 const settled=()=>new Promise(resolve=>document.addEventListener('htmx:afterSettle',function done(event){if(event.detail.target?.id==='route-editor'){document.removeEventListener('htmx:afterSettle',done);resolve()}}));
 try{
  const card=document.getElementById('mobile-route-0');const copy=card.querySelector('textarea').value;
  result.callsBefore=Number(await(await fetch('/measurement-count')).text());
  const opened=settled();document.querySelector('a[href*="action=add"]').click();await opened;
  const choice=document.querySelector('#route-editor input[name="destination"]');choice.checked=true;choice.form.requestSubmit();
  for(let i=0;i<200&&!document.getElementById('mobile-route-1');i++)await delay(20);
  const after=document.getElementById('mobile-route-0');
  result.same=card===after;result.copy=copy===after.querySelector('textarea').value;result.attribution=!!after.querySelector('.mobile-route-attribution');
  result.move=!!after.querySelector('a[href*="action=move"]');result.cards=document.querySelectorAll('.mobile-route-card').length;
  result.callsAfter=Number(await(await fetch('/measurement-count')).text());
 }catch(error){result.error=String(error)}
 document.body.innerHTML='<pre id="browser-result"></pre>';document.getElementById('browser-result').textContent=JSON.stringify(result);
 });</script>`
		_, _ = fmt.Fprint(w, strings.Replace(html, "</body>", script+"</body>", 1))
	})
	raw := runRouteBrowser(t, browser, mux)
	var result struct {
		Same, Copy, Attribution, Move  bool
		Cards, CallsBefore, CallsAfter int
		Error                          string
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || !result.Same || !result.Copy || !result.Attribution || !result.Move || result.Cards != 2 || result.CallsBefore == 0 || result.CallsAfter != result.CallsBefore {
		t.Fatalf("mobile timing preservation: %s", raw)
	}
}

func TestAddingSecondDriverBrowserPreservesMeasuredTimings(t *testing.T) {
	browser := os.Getenv("BROWSER_TEST_BINARY")
	if browser == "" {
		t.Skip("BROWSER_TEST_BINARY not set")
	}
	h, snapshot, _ := oneRouteWithUnusedDriver(t)
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

func TestMobileLocationBrowserPreservesOffPageChoiceThroughDone(t *testing.T) {
	browser := os.Getenv("BROWSER_TEST_BINARY")
	if browser == "" {
		t.Skip("BROWSER_TEST_BINARY not set")
	}
	h, db := newTestPageHandler(t)
	h.PlanDraft = plandraft.NewStore()
	t.Cleanup(h.PlanDraft.Close)
	id := h.PlanDraft.NewID()
	h.PlanDraft.Update(id, func(*plandraft.Draft) {})
	cookie := mobileTestCookie(id)
	for i := 1; i <= 60; i++ {
		if _, err := db.ActivityLocations().Create(t.Context(), &models.ActivityLocation{Name: fmt.Sprintf("Location %03d", i), Address: "1 Main St"}); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("../../web/static"))))
	mux.HandleFunc("/m/plan/location", func(w http.ResponseWriter, r *http.Request) { r.AddCookie(cookie); h.HandleMobileLocation(w, r) })
	mux.HandleFunc("/m", func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(cookie)
		_, draft, _, err := h.mobileDraft(w, r)
		if err != nil {
			http.Error(w, "draft", 500)
			return
		}
		_, _ = fmt.Fprintf(w, `<pre id="browser-result">{"location":%d}</pre>`, draft.LocationID)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(cookie)
		recorder := httptest.NewRecorder()
		h.HandleMobileLocation(recorder, r)
		html := strings.ReplaceAll(recorder.Body.String(), `<script src="`+web.AssetURL("js/auth.js")+`" defer></script>`, "")
		script := `<script>addEventListener('load',async()=>{
 const settled=()=>new Promise(resolve=>document.addEventListener('htmx:afterSettle',function done(event){if(event.detail.target?.id==='mobile-location-results'){document.removeEventListener('htmx:afterSettle',done);resolve()}}));
 try{
  let done=settled();Array.from(document.querySelectorAll('#mobile-location-results button')).find(button=>button.textContent==='Next').click();await done;
  document.querySelector('input[name="location_id"][value="51"]').click();
  const search=document.querySelector('input[name="search"]');
  done=settled();search.value='Location 001';search.dispatchEvent(new Event('input',{bubbles:true}));await done;
  done=settled();search.value='';search.dispatchEvent(new Event('input',{bubbles:true}));await done;
  const form=document.getElementById('mobile-location-picker');
  const response=await fetch(form.action,{method:'POST',body:new URLSearchParams(new FormData(form))});
  document.body.innerHTML=await response.text();
 }catch(error){document.body.innerHTML='<pre id="browser-result"></pre>';document.getElementById('browser-result').textContent=JSON.stringify({error:String(error)})}
 });</script>`
		_, _ = fmt.Fprint(w, strings.Replace(html, "</body>", script+"</body>", 1))
	})
	raw := runRouteBrowser(t, browser, mux)
	var result struct {
		Location int64
		Error    string
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || result.Location != 51 {
		t.Fatalf("location selection: %s", raw)
	}
}

func TestMobileResetBrowserConfirmsAndRemovesAddedCard(t *testing.T) {
	browser := os.Getenv("BROWSER_TEST_BINARY")
	if browser == "" {
		t.Skip("BROWSER_TEST_BINARY not set")
	}
	h, snapshot, cookie := oneRouteWithUnusedDriver(t)
	add := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/m/routes/add-driver", strings.NewReader("session_id="+snapshot.ID+"&driver_id=2"))
	add.AddCookie(cookie)
	add.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.HandleMobileAddDriver(httptest.NewRecorder(), add)
	var resets atomic.Int64
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("../../web/static"))))
	mux.HandleFunc("/probe", func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, resets.Load()) })
	mux.HandleFunc("/m/routes/reset", func(w http.ResponseWriter, r *http.Request) {
		resets.Add(1)
		r.AddCookie(cookie)
		h.HandleMobileReset(w, r)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(cookie)
		recorder := httptest.NewRecorder()
		h.HandleMobileRoutes(recorder, r)
		html := strings.ReplaceAll(recorder.Body.String(), `<script src="`+web.AssetURL("js/auth.js")+`" defer></script>`, "")
		script := `<script>addEventListener('load',async()=>{
 const result={confirmations:0};
 const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
 try{
  const form=document.querySelector('form[action="/m/routes/reset"]');
  document.querySelector('[name="notes"]').value='Keep these notes';
  form.requestSubmit();await delay(20);
  if(document.querySelector('.confirm-overlay.is-open'))result.confirmations++;
  document.querySelector('[data-confirm-action="cancel"]').click();await delay(100);
  result.afterCancel=Number(await(await fetch('/probe')).text());
  form.requestSubmit();await delay(20);
  if(document.querySelector('.confirm-overlay.is-open'))result.confirmations++;
  document.querySelector('[data-confirm-action="confirm"]').click();
  for(let i=0;i<200&&document.getElementById('mobile-route-1');i++)await delay(20);
  result.staleCard=!!document.getElementById('mobile-route-1');
  result.afterConfirm=Number(await(await fetch('/probe')).text());
  result.notes=document.querySelector('[name="notes"]').value;
 }catch(error){result.error=String(error)}
 document.body.innerHTML='<pre id="browser-result"></pre>';document.getElementById('browser-result').textContent=JSON.stringify(result);
 });</script>`
		_, _ = fmt.Fprint(w, strings.Replace(html, "</body>", script+"</body>", 1))
	})
	raw := runRouteBrowser(t, browser, mux)
	var result struct {
		Confirmations, AfterCancel, AfterConfirm int
		StaleCard                                bool
		Notes, Error                             string
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || result.Confirmations != 2 || result.AfterCancel != 0 || result.AfterConfirm != 1 || result.StaleCard || result.Notes != "Keep these notes" {
		t.Fatalf("reset confirmation: %s", raw)
	}
}
