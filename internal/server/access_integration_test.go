package server

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"mime/multipart"
	"net/http"
	"reflect"
	"ride-home-router/internal/access/accesstest"
	"ride-home-router/internal/postgres/postgrestest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type accessHTTP struct {
	t      *testing.T
	client *http.Client
}

func (h accessHTTP) request(base, method, path, auth, body, contentType string, headers map[string]string) (int, string, http.Header) {
	h.t.Helper()
	r, err := http.NewRequestWithContext(h.t.Context(), method, base+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	if contentType == "" {
		contentType = "application/json"
	}
	r.Header.Set("Content-Type", contentType)
	r.Header.Set("Origin", base)
	if auth != "" {
		r.Header.Set("Authorization", "Bearer "+auth)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	response, err := h.client.Do(r)
	if err != nil {
		h.t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		h.t.Fatal(err)
	}
	return response.StatusCode, string(data), response.Header
}

func startAccessServer(t *testing.T, databaseURL string, f *accesstest.Fixture) (*Server, string) {
	t.Helper()
	s, err := New(t.Context(), Config{Addr: "127.0.0.1:0", DatabaseURL: databaseURL, Auth: f.Config("admin@example.test", "second@example.test")})
	if err != nil {
		t.Fatal(err)
	}
	addr, err := s.Start()
	if err != nil {
		_ = s.Shutdown(t.Context())
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return s, "http://" + addr
}

// Capture complete sorted table contents, including counts, settings, approval rows,
// workflow payloads and import jobs. This catches updates as well as insertions.
func accessDatabaseSnapshot(t *testing.T, conn *pgx.Conn) map[string]string {
	t.Helper()
	rows, err := conn.Query(t.Context(), `SELECT tablename FROM pg_tables WHERE schemaname=current_schema() ORDER BY tablename`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	result := map[string]string{}
	for _, table := range tables {
		var data string
		query := `SELECT COALESCE(jsonb_agg(data ORDER BY data::text),'[]'::jsonb)::text FROM (SELECT to_jsonb(t) AS data FROM ` + pgx.Identifier{table}.Sanitize() + ` t) snapshot`
		if err := conn.QueryRow(t.Context(), query).Scan(&data); err != nil {
			t.Fatal(err)
		}
		result[table] = data
	}
	return result
}

func assertAccessDenied(t *testing.T, status int, body string, headers http.Header, want int, method string) {
	t.Helper()
	if status != want {
		t.Fatalf("status %d want %d body=%q", status, want, body)
	}
	if method != http.MethodHead && strings.TrimSpace(body) != http.StatusText(want) {
		t.Fatalf("denial leaked non-generic content: %q", body)
	}
	if len(headers.Values("Set-Cookie")) != 0 {
		t.Fatalf("denial created cookies: %v", headers.Values("Set-Cookie"))
	}
	if headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("denial cache policy %q", headers.Get("Cache-Control"))
	}
}

func TestAccessAcrossInstancesAndRevocation(t *testing.T) {
	databaseURL := postgrestest.DatabaseURL(t)
	f := accesstest.New(t)
	admin := f.Admin()
	f.User("second", []string{"SECOND@example.test"}, nil)
	f.Session("second_session", "second", "active")
	second := f.Token("second", "second_session")
	f.User("member", []string{"Member@example.test"}, nil)
	f.Session("member_session", "member", "active")
	member := f.Token("member", "member_session", map[string]any{"public_metadata": map[string]any{"role": "admin"}, "org_role": "org:admin"})
	first, a := startAccessServer(t, databaseURL, f)
	_, b := startAccessServer(t, databaseURL, f)
	h := accessHTTP{t, &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	conn, err := pgx.Connect(t.Context(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	for _, token := range []string{admin, second} {
		for _, base := range []string{a, b} {
			status, body, _ := h.request(base, "GET", "/api/v1/access", token, "", "", nil)
			if status != 200 {
				t.Fatalf("configured admin denied: %d %s", status, body)
			}
		}
	}
	status, body, _ := h.request(a, "POST", "/api/v1/access", admin, `{"email":"  MEMBER@Example.Test  "}`, "", nil)
	if status != 200 || !strings.Contains(body, "member@example.test") {
		t.Fatalf("normalized grant: %d %s", status, body)
	}
	before := accessDatabaseSnapshot(t, conn)
	for _, base := range []string{a, b} {
		for _, method := range []string{"GET", "POST", "DELETE"} {
			status, body, headers := h.request(base, method, "/api/v1/access", member, `{"email":"attacker@example.test"}`, "", map[string]string{"X-Admin": "true", "X-User-Role": "admin"})
			assertAccessDenied(t, status, body, headers, 403, method)
		}
	}
	if !reflect.DeepEqual(before, accessDatabaseSnapshot(t, conn)) {
		t.Fatal("non-admin access management changed database")
	}
	status, body, _ = h.request(a, "POST", "/api/v1/labels", member, `{"name":"protected synthetic label"}`, "", nil)
	if status != 201 {
		t.Fatalf("member mutation: %d %s", status, body)
	}
	for _, path := range []string{"/labels", "/api/v1/labels", "/settings", "/m/people"} {
		status, body, _ = h.request(b, "GET", path, member, "", "", nil)
		if status != 200 {
			t.Fatalf("member page %s: %d %s", path, status, body)
		}
		if (path == "/labels" || path == "/api/v1/labels") && !strings.Contains(body, "protected synthetic label") {
			t.Fatalf("shared label missing at %s", path)
		}
	}
	status, body, _ = h.request(b, "POST", "/api/v1/labels", member, "name=protected+HTMX+label", "application/x-www-form-urlencoded", map[string]string{"HX-Request": "true"})
	if status != 200 || !strings.Contains(body, "protected HTMX label") {
		t.Fatalf("HTMX mutation: %d %s", status, body)
	}
	upload, contentType := accessImportBody(t)
	status, body, _ = h.request(a, "POST", "/api/v1/imports", member, upload, contentType, map[string]string{"HX-Request": "true"})
	if status != 201 {
		t.Fatalf("multipart import: %d %s", status, body)
	}
	// Persist a mobile workflow using the same authenticated HTTP entry point.
	status, body, _ = h.request(a, "POST", "/m/plan/when", member, "route_time=06%3A45&mode=pickup", "application/x-www-form-urlencoded", nil)
	if status != 303 {
		t.Fatalf("draft: %d %s", status, body)
	}
	status, body, _ = h.request(b, "DELETE", "/api/v1/access", second, `{"email":"member@example.test"}`, "", nil)
	if status != 200 {
		t.Fatalf("revoke: %d %s", status, body)
	}
	before = accessDatabaseSnapshot(t, conn)
	for _, base := range []string{a, b} {
		status, body, headers := h.request(base, "GET", "/api/v1/labels", member, "", "", nil)
		assertAccessDenied(t, status, body, headers, 403, "GET")
	}
	if err := first.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, restarted := startAccessServer(t, databaseURL, f)
	status, body, headers := h.request(restarted, "GET", "/api/v1/labels", member, "", "", nil)
	assertAccessDenied(t, status, body, headers, 403, "GET")
	if !reflect.DeepEqual(before, accessDatabaseSnapshot(t, conn)) {
		t.Fatal("denied revoked identity changed database")
	}
	// Regrant the same token, then revoke its live Clerk session without re-signing.
	status, body, _ = h.request(b, "POST", "/api/v1/access", admin, `{"email":"member@example.test"}`, "", nil)
	if status != 200 {
		t.Fatalf("regrant: %d %s", status, body)
	}
	status, body, _ = h.request(b, "GET", "/api/v1/labels", member, "", "", nil)
	if status != 200 {
		t.Fatalf("regrant not live: %d %s", status, body)
	}
	// A new revoked identity has no cached active status on either instance.
	member = f.Token("member", "revoked_member_session")
	f.Session("revoked_member_session", "member", "revoked")
	before = accessDatabaseSnapshot(t, conn)
	for _, base := range []string{b, restarted} {
		status, body, headers = h.request(base, "POST", "/api/v1/labels", member, `{"name":"must not exist"}`, "", nil)
		assertAccessDenied(t, status, body, headers, 401, "POST")
	}
	if !reflect.DeepEqual(before, accessDatabaseSnapshot(t, conn)) {
		t.Fatal("revoked Clerk session wrote data")
	}
}

func accessImportBody(t *testing.T) (string, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("kind", "participant"); err != nil {
		t.Fatal(err)
	}
	file, err := w.CreateFormFile("file", "synthetic.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(file, "name,address\nProtected synthetic rider,1 Synthetic Street\n"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return body.String(), w.FormDataContentType()
}

// Discover every mux registration, requiring literal patterns so new computed
// registrations cannot silently fall outside this security test.
func accessRoutePaths(t *testing.T) []string {
	t.Helper()
	paths := map[string]bool{}
	for _, source := range []struct{ file, function string }{{"server.go", "setupRoutes"}, {"../access/http.go", "Register"}} {
		file, err := parser.ParseFile(token.NewFileSet(), source.file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != source.function {
				continue
			}
			found = true
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") {
					return true
				}
				if len(call.Args) == 0 {
					t.Fatal("empty route registration")
				}
				literal, ok := call.Args[0].(*ast.BasicLit)
				if !ok {
					t.Fatal("computed route registration requires explicit coverage")
				}
				pattern, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Fatal(err)
				}
				fields := strings.Fields(pattern)
				path := fields[len(fields)-1]
				paths[path] = true
				if strings.HasSuffix(path, "/") && path != "/" {
					paths[path+"1"] = true
					paths[path+"1/edit"] = true
				}
				return true
			})
		}
		if !found {
			t.Fatalf("missing route function %s", source.function)
		}
	}
	for _, path := range []string{"/future/private", "/api/v1/future", "/api/v1/events/1/export", "/api/v1/exports", "/static/../participants", "/static/%2e%2e/participants", "/sign-in/../settings", "//participants", "/api/v1/imports/fixture/mapping", "/api/v1/imports/fixture/selection", "/api/v1/imports/fixture/commit", "/api/v1/imports/fixture/upload"} {
		paths[path] = true
	}
	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

func TestAccessDenialRouteMatrix(t *testing.T) {
	databaseURL := postgrestest.DatabaseURL(t)
	f := accesstest.New(t)
	admin := f.Admin()
	f.User("unapproved", []string{"unapproved@example.test"}, nil)
	f.Session("unapproved_session", "unapproved", "active")
	f.User("unverified", []string{"unapproved@example.test"}, []string{"admin@example.test"})
	f.Session("unverified_session", "unverified", "active")
	f.User("banned_user", []string{"admin@example.test"}, nil, map[string]any{"banned": true})
	f.User("locked_user", []string{"admin@example.test"}, nil, map[string]any{"locked": true})
	f.User("mismatched_user", []string{"admin@example.test"}, nil, map[string]any{"id": "other_user"})
	for _, id := range []string{"banned_user", "locked_user", "mismatched_user", "deleted_user"} {
		f.Session(id+"_session", id, "active")
	}
	f.Session("mismatch", "other_user", "active")
	f.Session("revoked", "user_admin", "revoked")
	f.Session("user_failure", "missing_user", "active")
	f.Fail("/v1/users/missing_user", 503)
	f.Fail("/v1/sessions/service_failure", 503)
	_, base := startAccessServer(t, databaseURL, f)
	h := accessHTTP{t, &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	// Seed protected data so response assertions are not vacuous.
	status, body, _ := h.request(base, "POST", "/api/v1/labels", admin, `{"name":"protected matrix secret"}`, "", nil)
	if status != 201 {
		t.Fatalf("seed: %d %s", status, body)
	}
	status, body, _ = h.request(base, "POST", "/m/plan/when", admin, "route_time=07%3A35&mode=pickup", "application/x-www-form-urlencoded", nil)
	if status != 303 {
		t.Fatalf("seed workflow: %d %s", status, body)
	}
	conn, err := pgx.Connect(t.Context(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	cases := []struct {
		name, token string
		status      int
	}{
		{"malformed", "not.a.valid.token", 401},
		{"missing_azp", f.Token("user_admin", "sess_admin", map[string]any{"azp": nil}), 401},
		{"missing_exp", f.Token("user_admin", "sess_admin", map[string]any{"exp": nil}), 401},
		{"missing_nbf", f.Token("user_admin", "sess_admin", map[string]any{"nbf": nil}), 401},
		{"missing_iat", f.Token("user_admin", "sess_admin", map[string]any{"iat": nil}), 401},
		{"pending", f.Token("user_admin", "sess_admin", map[string]any{"sts": "pending"}), 401},
		{"missing", "", 401},
		{"forged", accesstest.New(t).Token("user_admin", "sess_admin"), 401},
		{"expired", f.Token("user_admin", "sess_admin", map[string]any{"exp": time.Now().Add(-time.Hour).Unix()}), 401},
		{"issuer", f.Token("user_admin", "sess_admin", map[string]any{"iss": "https://attacker.example"}), 401},
		{"azp", f.Token("user_admin", "sess_admin", map[string]any{"azp": "https://attacker.example"}), 401},
		{"future_nbf", f.Token("user_admin", "sess_admin", map[string]any{"nbf": time.Now().Add(time.Hour).Unix()}), 401},
		{"missing_session_claim", f.Token("user_admin", "", nil), 401},
		{"unknown_session", f.Token("user_admin", "unknown"), 401},
		{"mismatched_session", f.Token("user_admin", "mismatch"), 401},
		{"revoked_session", f.Token("user_admin", "revoked"), 401},
		{"banned_user", f.Token("banned_user", "banned_user_session"), 401},
		{"locked_user", f.Token("locked_user", "locked_user_session"), 401},
		{"deleted_user", f.Token("deleted_user", "deleted_user_session"), 401},
		{"mismatched_user", f.Token("mismatched_user", "mismatched_user_session"), 401},
		{"session_service_failure", f.Token("user_admin", "service_failure"), 503},
		{"user_service_failure", f.Token("missing_user", "user_failure"), 503},
		{"unapproved", f.Token("unapproved", "unapproved_session", map[string]any{"role": "admin"}), 403},
		{"unverified_admin", f.Token("unverified", "unverified_session"), 403},
	}
	paths := accessRoutePaths(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			local := h
			local.t = t
			before := accessDatabaseSnapshot(t, conn)
			for _, path := range paths {
				for _, method := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
					// These exact read-only paths are deliberately public. Their writes are tested.
					if (method == "GET" || method == "HEAD") && (path == "/sign-in" || path == "/auth/config" || path == "/api/v1/health" || path == "/api/v1/ready" || (strings.HasPrefix(path, "/static/") && !strings.Contains(path, "..") && !strings.Contains(path, "%2e"))) {
						continue
					}
					t.Run(method+path, func(t *testing.T) {
						requester := local
						requester.t = t
						status, body, headers := requester.request(base, method, path, tc.token, `{"name":"denied mutation","use_miles":false}`, "", map[string]string{"HX-Request": "true", "X-Admin": "true"})
						assertAccessDenied(t, status, body, headers, tc.status, method)
						if headers.Get("HX-Reswap") != "none" {
							t.Fatal("HTMX denial may swap protected page")
						}
					})
				}
			}
			status, body, headers := local.request(base, "GET", "/participants", tc.token, "", "", map[string]string{"Accept": "text/html"})
			if tc.status == http.StatusServiceUnavailable {
				assertAccessDenied(t, status, body, headers, tc.status, "GET")
				if headers.Get("Location") != "" {
					t.Fatal("Clerk outage redirected browser")
				}
			} else if status != http.StatusSeeOther || !strings.HasPrefix(headers.Get("Location"), "/sign-in") || len(headers.Values("Set-Cookie")) != 0 || strings.Contains(body, "protected matrix secret") {
				t.Fatalf("browser denial: status %d headers=%v body=%q", status, headers, body)
			}
			upload, contentType := accessImportBody(t)
			status, body, headers = local.request(base, "POST", "/api/v1/imports", tc.token, upload, contentType, nil)
			assertAccessDenied(t, status, body, headers, tc.status, "POST")
			if !reflect.DeepEqual(before, accessDatabaseSnapshot(t, conn)) {
				t.Fatal("denied route matrix changed database contents")
			}
		})
	}
}
