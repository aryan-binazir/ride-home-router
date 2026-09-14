package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"ride-home-router/internal/models"
	"ride-home-router/internal/plandraft"
	"strings"
	"sync/atomic"
	"testing"
)

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
		html := strings.ReplaceAll(recorder.Body.String(), `<script src="/static/js/auth.js?v=20260912-theme" defer></script>`, "")
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
		html := strings.ReplaceAll(recorder.Body.String(), `<script src="/static/js/auth.js?v=20260912-theme" defer></script>`, "")
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
