package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"ride-home-router/internal/importer"
	"ride-home-router/internal/models"
	"ride-home-router/internal/plandraft"
	"ride-home-router/web"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Real Chromium + HTTP + application templates/scripts and route-edit handlers.
// The calculation fixture avoids external provider work; edits use the real store.
func TestRouteEditorBrowserLoadsSearchesAndMovesOnDemand(t *testing.T) {
	browser := os.Getenv("BROWSER_TEST_BINARY")
	if browser == "" {
		t.Skip("BROWSER_TEST_BINARY not set")
	}
	h, snapshot, _ := largeRouteFixture(t)
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("../../web/static"))))
	mux.HandleFunc("/api/v1/routes/editor", h.HandleRouteEditor)
	mux.HandleFunc("/api/v1/routes/edit/move-participant", h.HandleMoveParticipant)
	mux.HandleFunc("/api/v1/routes/calculate", func(w http.ResponseWriter, r *http.Request) {
		h.renderTemplate(w, "route_results", h.buildTimedRouteResultsView(snapshot, h.routeTimings(r.Context(), snapshot, nil)))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		view := IndexPageView{ActivePage: ActivePageHome, ActivityLocations: []models.ActivityLocation{*snapshot.ActivityLocation}}
		view.ActivityLocations[0].ID = 1
		view.SelectedLocation = &view.ActivityLocations[0]
		for _, route := range snapshot.Routes {
			view.Drivers = append(view.Drivers, *route.Driver)
			view.Participants = append(view.Participants, *route.Stops[0].Participant)
		}
		var page bytes.Buffer
		if err := h.Renderer.Render(&page, "index.html", view); err != nil {
			t.Error(err)
			http.Error(w, "render", 500)
			return
		}
		html := strings.ReplaceAll(page.String(), `<script src="`+web.AssetURL("js/auth.js")+`" defer></script>`, "")
		script := `<script>
addEventListener('load',async()=>{
 const result={errors:[],maxLongTaskMs:0};addEventListener('error',e=>result.errors.push(e.message));
 new PerformanceObserver(list=>{for(const entry of list.getEntries())result.maxLongTaskMs=Math.max(result.maxLongTaskMs,entry.duration)}).observe({type:'longtask',buffered:true});
 const settled=()=>new Promise(resolve=>document.addEventListener('htmx:afterSettle',function done(e){if(e.detail.target?.id==='route-editor'){document.removeEventListener('htmx:afterSettle',done);resolve();}}));
 const until=(predicate)=>new Promise((resolve,reject)=>{const start=Date.now();const tick=()=>{if(predicate())return resolve();if(Date.now()-start>10000)return reject(new Error('Timed out'));setTimeout(tick,20)};tick()});
 try {
 document.querySelector('[name="activity_location_id"]').value='1';
 selectAllParticipants();selectAllDrivers();
 const started=performance.now();document.getElementById('calculate-btn').click();await until(()=>document.querySelector('.routes-container'));result.routeReadyMs=performance.now()-started;
 result.initialOptions=document.querySelectorAll('.routes-container option').length;
 const untouched=document.querySelector('.route-card[data-route-index="250"]');
 const opened=settled();document.querySelector('.stop-actions button').click();await opened;
 result.choices=document.querySelectorAll('#route-editor [name="destination"]').length;
 const search=document.getElementById('route-editor-search');search.value='Driver 500';const searched=settled();search.form.requestSubmit();await searched;
 result.searchName=document.querySelector('#route-editor .route-editor-choice strong').textContent;
 document.querySelector('#route-editor [name="destination"]').click();document.querySelector('#route-editor [name="destination"]').form.requestSubmit();
 await until(()=>document.querySelector('.route-card[data-route-index="499"] .stop-item[data-participant-id="1"]'));
 result.moved=true;result.untouched=untouched===document.querySelector('.route-card[data-route-index="250"]');result.remainingEditors=document.querySelectorAll('#route-editor [name="destination"]').length;
 }catch(e){result.error=String(e)}
 document.body.innerHTML='<pre id="browser-result"></pre>';document.getElementById('browser-result').textContent=JSON.stringify(result);
});</script>`
		_, _ = fmt.Fprint(w, strings.Replace(html, "</body>", script+"</body>", 1))
	})
	raw := runRouteBrowser(t, browser, mux)
	t.Logf("500-route Chromium evidence: %s", raw)
	var result struct {
		Errors                                    []string
		Error                                     string
		InitialOptions, Choices, RemainingEditors int
		Moved, Untouched                          bool
		SearchName                                string
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || len(result.Errors) > 0 || result.InitialOptions != 0 || result.Choices != 25 || result.SearchName != "Driver 500" || !result.Moved || !result.Untouched || result.RemainingEditors != 0 {
		t.Fatalf("browser: %s", raw)
	}
}

func TestVehicleAssignmentBrowserRestoresWithoutLoadingCatalog(t *testing.T) {
	browser := os.Getenv("BROWSER_TEST_BINARY")
	if browser == "" {
		t.Skip("BROWSER_TEST_BINARY not set")
	}
	h, store := newTestPageHandler(t)
	for i := range 60 {
		if _, err := store.Drivers().Create(t.Context(), &models.Driver{Name: fmt.Sprintf("A driver %02d", i), Address: "1 Test St", VehicleCapacity: 4}); err != nil {
			t.Fatal(err)
		}
	}
	driver, err := store.Drivers().Create(t.Context(), &models.Driver{Name: "ZZ Driver", Address: "1 Test St", VehicleCapacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	vehicle, err := store.OrganizationVehicles().Create(t.Context(), &models.OrganizationVehicle{Name: "Selected van", Capacity: 12})
	if err != nil {
		t.Fatal(err)
	}
	draft, _ := json.Marshal(map[string]any{"driverIds": []string{fmt.Sprint(driver.ID)}, "participantIds": []string{}, "vanAssignments": map[string]string{fmt.Sprint(driver.ID): fmt.Sprint(vehicle.ID)}, "mode": "dropoff", "routeTime": "18:30"})
	draftJSON, _ := json.Marshal(string(draft))
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("../../web/static"))))
	mux.HandleFunc("/api/v1/planner/vehicle-assignments", h.HandleVehicleAssignments)
	mux.HandleFunc("/api/v1/planner/drivers", h.HandlePlannerPicker)
	mux.HandleFunc("/api/v1/planner/participants", h.HandlePlannerPicker)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		recorder := httptest.NewRecorder()
		h.HandleIndexPage(recorder, r)
		html := strings.ReplaceAll(recorder.Body.String(), `<script src="`+web.AssetURL("js/auth.js")+`" defer></script>`, "")
		html = strings.Replace(html, "<head>", "<head><script>localStorage.setItem('ride-home-router:event-planner-draft:v1',"+string(draftJSON)+");</script>", 1)
		script := `<script>addEventListener('load',()=>{let attempts=0;const check=()=>{const id=document.querySelector('.driver-checkbox:checked')?.value;const select=document.getElementById('van-assignment-'+id);if(select?.value||attempts++>100){const result={value:select?.value,capacity:select?.selectedOptions[0]?.dataset.capacity,options:document.querySelectorAll('.van-assignment-select option').length};document.body.innerHTML='<pre id="browser-result"></pre>';document.getElementById('browser-result').textContent=JSON.stringify(result);}else setTimeout(check,20)};check()});</script>`
		_, _ = fmt.Fprint(w, strings.Replace(html, "</body>", script+"</body>", 1))
	})
	raw := runRouteBrowser(t, browser, mux)
	var result struct {
		Value, Capacity string
		Options         int
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if result.Value != fmt.Sprint(vehicle.ID) || result.Capacity != "12" || result.Options != 51 {
		t.Fatalf("restore: %s", raw)
	}
}

func TestPlannerPagingBrowserKeepsSelectionsAndSelectsAllMatches(t *testing.T) {
	browser := os.Getenv("BROWSER_TEST_BINARY")
	if browser == "" {
		t.Skip("BROWSER_TEST_BINARY not set")
	}
	h, store := newTestPageHandler(t)
	for i := 1; i <= 60; i++ {
		if _, err := store.Drivers().Create(t.Context(), &models.Driver{Name: fmt.Sprintf("Driver %03d", i), Address: "1 Test St", VehicleCapacity: 4}); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("../../web/static"))))
	mux.HandleFunc("/api/v1/planner/drivers", h.HandlePlannerPicker)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		recorder := httptest.NewRecorder()
		h.HandleIndexPage(recorder, r)
		html := strings.ReplaceAll(recorder.Body.String(), `<script src="`+web.AssetURL("js/auth.js")+`" defer></script>`, "")
		script := `<script>addEventListener('load',async()=>{const result={};try{document.querySelector('.driver-checkbox').click();await requestPlannerPicker('drivers',50);const visible=document.querySelectorAll('#drivers-selection .driver-checkbox');visible[visible.length-1].click();await requestPlannerPicker('drivers',0);result.selected=document.querySelectorAll('.driver-checkbox:checked').length;result.seats=document.getElementById('drivers-selected-seats').textContent;await selectAllDrivers();result.all=document.querySelectorAll('.driver-checkbox:checked').length;result.rows=document.querySelectorAll('#drivers-selection .select-row').length;}catch(e){result.error=String(e)}document.body.innerHTML='<pre id="browser-result"></pre>';document.getElementById('browser-result').textContent=JSON.stringify(result);});</script>`
		_, _ = fmt.Fprint(w, strings.Replace(html, "</body>", script+"</body>", 1))
	})
	raw := runRouteBrowser(t, browser, mux)
	var result struct {
		Selected, All, Rows int
		Seats, Error        string
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || result.Selected != 2 || result.Seats != "8" || result.All != 60 || result.Rows != 50 {
		t.Fatalf("paging: %s", raw)
	}
}

func TestMobileVehicleChoiceAndPagingBrowser(t *testing.T) {
	browser := os.Getenv("BROWSER_TEST_BINARY")
	if browser == "" {
		t.Skip("BROWSER_TEST_BINARY not set")
	}
	h, store := newTestPageHandler(t)
	h.PlanDraft = plandraft.NewStore()
	t.Cleanup(h.PlanDraft.Close)
	for i := 1; i <= 60; i++ {
		if _, err := store.Drivers().Create(t.Context(), &models.Driver{Name: fmt.Sprintf("Driver %03d", i), Address: "1 Test St", VehicleCapacity: 4}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.OrganizationVehicles().Create(t.Context(), &models.OrganizationVehicle{Name: "Test van", Capacity: 12}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("../../web/static"))))
	mux.HandleFunc("/api/v1/planner/vehicle-editor", h.HandleVehicleEditor)
	mux.HandleFunc("/m/plan/drivers", h.HandleMobileDrivers)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		recorder := httptest.NewRecorder()
		h.HandleMobileDrivers(recorder, r)
		for key, values := range recorder.Header() {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		html := strings.ReplaceAll(recorder.Body.String(), `<script src="`+web.AssetURL("js/auth.js")+`" defer></script>`, "")
		script := `<script>addEventListener('load',async()=>{const result={};const until=async(fn)=>{for(let n=0;n<200;n++){if(fn())return;await new Promise(r=>setTimeout(r,20))}throw Error('condition timeout')};try{result.initial=document.querySelectorAll('.mobile-driver-choice').length;document.querySelector('input[name="driver_ids"]').click();const control=document.querySelector('.van-assignment-inline');result.visible=!control.classList.contains('hidden');control.querySelector('button').click();await until(()=>document.querySelector('#route-editor input[name="vehicle_id"][value="1"]'));await new Promise(r=>setTimeout(r,100));const radio=document.querySelector('#route-editor input[name="vehicle_id"][value="1"]');radio.checked=true;radio.form.requestSubmit();await until(()=>document.querySelector('#mobile-van-assignment-1')?.value==='1');result.seats=document.getElementById('mobile-selected-seats').textContent;const next=Array.from(document.querySelectorAll('#mobile-driver-results button')).find(b=>b.textContent==='Next');if(!next)throw Error('missing next page');next.click();await until(()=>document.querySelectorAll('.mobile-driver-choice').length===10);document.querySelector('.mobile-driver-choice input[name="driver_ids"]').click();result.after=document.getElementById('mobile-selected-seats').textContent;result.selected=new FormData(document.getElementById('mobile-driver-picker')).getAll('driver_ids').length;}catch(e){result.error=String(e)}document.body.innerHTML='<pre id="browser-result"></pre>';document.getElementById('browser-result').textContent=JSON.stringify(result);});</script>`
		_, _ = fmt.Fprint(w, strings.Replace(html, "</body>", script+"</body>", 1))
	})
	raw := runRouteBrowser(t, browser, mux)
	var result struct {
		Initial, Selected   int
		Visible             bool
		Seats, After, Error string
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || result.Initial != 50 || !result.Visible || result.Seats != "12 seats selected" || result.After != "16 seats selected" || result.Selected != 2 {
		t.Fatalf("mobile picker: %s", raw)
	}
}

func TestRosterPagingAndBulkLabelBrowser(t *testing.T) {
	browser := os.Getenv("BROWSER_TEST_BINARY")
	if browser == "" {
		t.Skip("BROWSER_TEST_BINARY not set")
	}
	h, store := newTestPageHandler(t)
	for i := 1; i <= 60; i++ {
		if _, err := store.Drivers().Create(t.Context(), &models.Driver{Name: fmt.Sprintf("Driver %03d", i), Address: "1 Test St", VehicleCapacity: 4}); err != nil {
			t.Fatal(err)
		}
	}
	label, err := store.Labels().Create(t.Context(), &models.Label{Name: "Trip"})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("../../web/static"))))
	mux.HandleFunc("/api/v1/drivers", h.HandleListDrivers)
	mux.HandleFunc("/api/v1/drivers/labels/add", h.HandleAddDriversToLabel)
	mux.HandleFunc("/api/v1/planner/label-editor", h.HandleBulkLabelEditor)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		recorder := httptest.NewRecorder()
		h.HandleDriversPage(recorder, r)
		html := strings.ReplaceAll(recorder.Body.String(), `<script src="`+web.AssetURL("js/auth.js")+`" defer></script>`, "")
		script := `<script>addEventListener('load',async()=>{const result={};const until=async(fn)=>{for(let n=0;n<300;n++){if(fn())return;await new Promise(r=>setTimeout(r,20))}throw Error('condition timeout')};const settled=id=>new Promise(resolve=>document.addEventListener('htmx:afterSettle',function done(e){if(e.detail.target?.id===id){document.removeEventListener('htmx:afterSettle',done);resolve()}}));try{document.querySelector('input[data-bulk-row]').click();let done=settled('drivers-list');Array.from(document.querySelectorAll('#drivers-list button')).find(b=>b.textContent==='Next').click();await done;const inputs=document.querySelectorAll('input[data-bulk-row]');inputs[inputs.length-1].click();done=settled('drivers-list');Array.from(document.querySelectorAll('#drivers-list button')).find(b=>b.textContent==='Previous').click();await done;result.selected=new FormData(document.getElementById('driver-bulk-form')).getAll('driver_ids').length;done=settled('route-editor');document.querySelector('[hx-get*="label-editor"]').click();await done;document.querySelector('#route-editor input[name="label_id"]').checked=true;document.querySelector('#route-editor input[name="label_id"]').form.requestSubmit();await until(()=>!document.getElementById('route-editor-dialog').open);result.remaining=new FormData(document.getElementById('driver-bulk-form')).getAll('driver_ids').length;result.rows=document.querySelectorAll('input[data-bulk-row]').length;}catch(e){result.error=String(e)}document.body.innerHTML='<pre id="browser-result"></pre>';document.getElementById('browser-result').textContent=JSON.stringify(result)});</script>`
		_, _ = fmt.Fprint(w, strings.Replace(html, "</body>", script+"</body>", 1))
	})
	raw := runRouteBrowser(t, browser, mux)
	var result struct {
		Selected, Remaining, Rows int
		Error                     string
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || result.Selected != 2 || result.Remaining != 2 || result.Rows != 50 {
		t.Fatalf("bulk roster: %s", raw)
	}
	labels, err := store.Labels().ListLabelIDsForDrivers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 2 || len(labels[1]) != 1 || labels[1][0] != label.ID || len(labels[60]) != 1 || labels[60][0] != label.ID {
		t.Fatalf("bulk action did not preserve off-page selection: %v", labels)
	}
}

func TestImportBrowserPagesAndPausesCollapsedPolling(t *testing.T) {
	browser := os.Getenv("BROWSER_TEST_BINARY")
	if browser == "" {
		t.Skip("BROWSER_TEST_BINARY not set")
	}
	h, _ := newImportTestHandler(t, &importTestGeocoder{})
	csv := "name,address\n"
	for i := 1; i <= 2000; i++ {
		csv += fmt.Sprintf("Rider %04d,1 Main St\n", i)
	}
	recorder := httptest.NewRecorder()
	h.HandleCreateImport(recorder, newImportPanelUploadRequest(t, "riders.csv", csv, importer.KindParticipant, ""))
	id := importPanelSessionID(t, recorder.Body.String())
	recorder = httptest.NewRecorder()
	h.HandleImportSession(recorder, newImportPanelFormRequest(http.MethodPut, "/api/v1/imports/"+id+"/mapping?view=panel", url.Values{"column_0": {"name"}, "column_1": {"address"}}))
	preview := recorder.Body.String()
	var polls atomic.Int64
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("../../web/static"))))
	mux.HandleFunc("/probe", func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, polls.Load()) })
	mux.HandleFunc("/api/v1/imports/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("progress") == "1" {
			polls.Add(1)
		}
		h.HandleImportSession(w, r)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		recorder := httptest.NewRecorder()
		h.HandleParticipantsPage(recorder, r)
		html := strings.ReplaceAll(recorder.Body.String(), `<script src="`+web.AssetURL("js/auth.js")+`" defer></script>`, "")
		html = strings.Replace(html, `<div id="import-steps" class="import-steps"></div>`, `<div id="import-steps" class="import-steps">`+preview+`</div>`, 1)
		script := `<script>addEventListener('load',async()=>{const result={};const delay=ms=>new Promise(r=>setTimeout(r,ms));const probe=async()=>Number(await(await fetch('/probe')).text());const settled=id=>new Promise(resolve=>document.addEventListener('htmx:afterSettle',function done(e){if(e.detail.target?.id===id){document.removeEventListener('htmx:afterSettle',done);resolve()}}));try{await delay(4200);result.closed=await probe();const panel=document.getElementById('import-panel');panel.open=true;await delay(4200);result.open=await probe();panel.open=false;await delay(4200);result.closedAgain=await probe();panel.open=true;await delay(100);let done=settled('import-commit-bar');document.querySelector('#import-selection-form input[name="selected"]').click();await done;done=settled('import-steps');Array.from(document.querySelectorAll('#import-steps nav button')).find(b=>b.textContent==='Next').click();await done;result.rows=document.querySelectorAll('#import-selection-form input[name="selected"]').length;result.first=document.querySelector('#import-selection-form input[name="selected"]').value;result.summary=document.querySelector('.import-commit-summary').textContent;}catch(e){result.error=String(e)}document.body.innerHTML='<pre id="browser-result"></pre>';document.getElementById('browser-result').textContent=JSON.stringify(result)});</script>`
		_, _ = fmt.Fprint(w, strings.Replace(html, "</body>", script+"</body>", 1))
	})
	raw := runRouteBrowser(t, browser, mux)
	t.Logf("2000-row import Chromium evidence: %s", raw)
	var result struct {
		Closed, Open, ClosedAgain, Rows int
		First, Summary, Error           string
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || result.Closed != 0 || result.Open < 1 || result.ClosedAgain != result.Open || result.Rows != 50 || result.First != "50" || result.Summary != "1999 of 2000 rows selected" {
		t.Fatalf("import paging/polling: %s", raw)
	}
}

func runRouteBrowser(t *testing.T, browser string, mux *http.ServeMux) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("browser request: %s", r.URL.Path)
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	//nolint:gosec // Explicit opt-in browser binary for a synthetic local test server.
	cmd := exec.CommandContext(ctx, browser, "--headless", "--no-sandbox", "--disable-gpu", "--no-first-run", "--no-proxy-server", "--user-data-dir="+filepath.Join(dir, "profile"), "--dump-dom", "--virtual-time-budget=15000", server.URL)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("browser: %v", err)
	}
	start := strings.Index(string(output), `<pre id="browser-result">`)
	if start < 0 {
		t.Fatalf("browser did not complete (output %d bytes)", len(output))
	}
	raw, _, _ := strings.Cut(string(output)[start+len(`<pre id="browser-result">`):], "</pre>")
	return raw
}
