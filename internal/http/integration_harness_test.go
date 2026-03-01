package http_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/textproto"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"hopshare/internal/database"
	"hopshare/internal/database/migrate"
)

var (
	httpDBOnce        sync.Once
	sharedHTTPDB      *sql.DB
	httpDBSetupErr    error
	httpErrMissingURL = errors.New("HOPSHARE_DB_URL or DATABASE_URL not set")
)

const (
	testCSRFCookieName = "hopshare_csrf"
	testCSRFFieldName  = "csrf_token"
)

func requireHTTPTestDB(t *testing.T) *sql.DB {
	t.Helper()

	httpDBOnce.Do(func() {
		dbURL := os.Getenv("HOPSHARE_DB_URL")
		if dbURL == "" {
			dbURL = os.Getenv("DATABASE_URL")
		}
		if dbURL == "" {
			httpDBSetupErr = httpErrMissingURL
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		sharedHTTPDB, httpDBSetupErr = database.New(ctx, dbURL)
		if httpDBSetupErr != nil {
			return
		}

		httpDBSetupErr = migrate.Run(ctx, sharedHTTPDB)
	})

	if errors.Is(httpDBSetupErr, httpErrMissingURL) {
		t.Skip(httpErrMissingURL.Error())
	}
	if httpDBSetupErr != nil {
		t.Fatalf("database setup failed: %v", httpDBSetupErr)
	}
	return sharedHTTPDB
}

type testActor struct {
	t        *testing.T
	name     string
	baseURL  string
	email    string
	password string
	client   *http.Client
}

func newTestActor(t *testing.T, name, baseURL, email, password string) *testActor {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar for %s: %v", name, err)
	}

	return &testActor{
		t:        t,
		name:     name,
		baseURL:  baseURL,
		email:    email,
		password: password,
		client: &http.Client{
			Jar: jar,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

type multipartFile struct {
	FieldName   string
	FileName    string
	ContentType string
	Data        []byte
}

func (a *testActor) Login() {
	a.t.Helper()

	resp := a.PostForm("/login", url.Values{
		"email":    {a.email},
		"password": {a.password},
	})
	requireRedirectPath(a.t, resp, "/my-hopshare")
}

func (a *testActor) Get(path string) *http.Response {
	a.t.Helper()

	req, err := http.NewRequest(http.MethodGet, a.baseURL+path, nil)
	if err != nil {
		a.t.Fatalf("%s create GET %s failed: %v", a.name, path, err)
	}
	return a.Do(req)
}

func (a *testActor) Request(method, path string, body io.Reader, headers map[string]string) *http.Response {
	a.t.Helper()

	req, err := http.NewRequest(method, a.baseURL+path, body)
	if err != nil {
		a.t.Fatalf("%s create %s %s failed: %v", a.name, method, path, err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return a.Do(req)
}

func (a *testActor) Do(req *http.Request) *http.Response {
	a.t.Helper()

	resp, err := a.client.Do(req)
	if err != nil {
		a.t.Fatalf("%s request %s %s failed: %v", a.name, req.Method, req.URL.String(), err)
	}
	return resp
}

func (a *testActor) PostForm(path string, form url.Values) *http.Response {
	a.t.Helper()

	submission := make(url.Values, len(form)+1)
	for k, values := range form {
		copied := make([]string, len(values))
		copy(copied, values)
		submission[k] = copied
	}
	if submission.Get(testCSRFFieldName) == "" {
		submission.Set(testCSRFFieldName, a.ensureCSRFToken())
	}

	resp, err := a.client.PostForm(a.baseURL+path, submission)
	if err != nil {
		a.t.Fatalf("%s POST %s failed: %v", a.name, path, err)
	}
	return resp
}

func (a *testActor) PostMultipart(path string, fields map[string]string) *http.Response {
	a.t.Helper()
	return a.PostMultipartWithFiles(path, fields, nil)
}

func (a *testActor) PostMultipartWithFiles(path string, fields map[string]string, files []multipartFile) *http.Response {
	a.t.Helper()

	submissionFields := make(map[string]string, len(fields)+1)
	for k, v := range fields {
		submissionFields[k] = v
	}
	if strings.TrimSpace(submissionFields[testCSRFFieldName]) == "" {
		submissionFields[testCSRFFieldName] = a.ensureCSRFToken()
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for k, v := range submissionFields {
		if err := writer.WriteField(k, v); err != nil {
			a.t.Fatalf("%s write multipart field %q failed: %v", a.name, k, err)
		}
	}
	for _, f := range files {
		partHeaders := make(textproto.MIMEHeader)
		partHeaders.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, f.FieldName, f.FileName))
		if strings.TrimSpace(f.ContentType) != "" {
			partHeaders.Set("Content-Type", f.ContentType)
		}
		part, err := writer.CreatePart(partHeaders)
		if err != nil {
			a.t.Fatalf("%s create multipart file part %q failed: %v", a.name, f.FieldName, err)
		}
		if _, err := part.Write(f.Data); err != nil {
			a.t.Fatalf("%s write multipart file part %q failed: %v", a.name, f.FieldName, err)
		}
	}
	if err := writer.Close(); err != nil {
		a.t.Fatalf("%s close multipart writer failed: %v", a.name, err)
	}

	req, err := http.NewRequest(http.MethodPost, a.baseURL+path, &body)
	if err != nil {
		a.t.Fatalf("%s create multipart request %s failed: %v", a.name, path, err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := a.client.Do(req)
	if err != nil {
		a.t.Fatalf("%s POST multipart %s failed: %v", a.name, path, err)
	}
	return resp
}

func (a *testActor) ensureCSRFToken() string {
	a.t.Helper()

	if token := a.csrfToken(); token != "" {
		return token
	}

	req, err := http.NewRequest(http.MethodGet, a.baseURL+"/", nil)
	if err != nil {
		a.t.Fatalf("%s create GET / for csrf token failed: %v", a.name, err)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		a.t.Fatalf("%s GET / for csrf token failed: %v", a.name, err)
	}
	_ = resp.Body.Close()

	token := a.csrfToken()
	if token == "" {
		a.t.Fatalf("%s csrf cookie %q not set after bootstrap request", a.name, testCSRFCookieName)
	}
	return token
}

func (a *testActor) csrfToken() string {
	a.t.Helper()

	base, err := url.Parse(a.baseURL)
	if err != nil {
		a.t.Fatalf("%s parse base url %q failed: %v", a.name, a.baseURL, err)
	}
	for _, c := range a.client.Jar.Cookies(base) {
		if c.Name == testCSRFCookieName && strings.TrimSpace(c.Value) != "" {
			return c.Value
		}
	}
	return ""
}

func (a *testActor) cookieValue(name string) string {
	a.t.Helper()
	base, err := url.Parse(a.baseURL)
	if err != nil {
		a.t.Fatalf("%s parse base url %q failed: %v", a.name, a.baseURL, err)
	}
	for _, c := range a.client.Jar.Cookies(base) {
		if c.Name == name && strings.TrimSpace(c.Value) != "" {
			return c.Value
		}
	}
	return ""
}

func requireRedirectPath(t *testing.T, resp *http.Response, wantPath string) *url.URL {
	t.Helper()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		t.Fatalf("expected %d redirect to %s, got status=%d body=%q", http.StatusSeeOther, wantPath, resp.StatusCode, string(body))
	}

	loc, err := resp.Location()
	if err != nil {
		t.Fatalf("read redirect location: %v", err)
	}
	if loc.Path != wantPath {
		t.Fatalf("unexpected redirect path: got=%q want=%q full_location=%q", loc.Path, wantPath, loc.String())
	}

	return loc
}

func requireRedirectPathOneOf(t *testing.T, resp *http.Response, wantPaths ...string) *url.URL {
	t.Helper()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		t.Fatalf("expected %d redirect, got status=%d body=%q", http.StatusSeeOther, resp.StatusCode, string(body))
	}

	loc, err := resp.Location()
	if err != nil {
		t.Fatalf("read redirect location: %v", err)
	}
	for _, p := range wantPaths {
		if loc.Path == p {
			return loc
		}
	}
	t.Fatalf("unexpected redirect path: got=%q want_one_of=%q full_location=%q", loc.Path, strings.Join(wantPaths, ","), loc.String())
	return nil
}

func requireStatus(t *testing.T, resp *http.Response, wantStatus int) string {
	t.Helper()
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != wantStatus {
		t.Fatalf("expected status=%d got=%d body=%q", wantStatus, resp.StatusCode, string(body))
	}
	return string(body)
}

func requireBodyContains(t *testing.T, body string, want string) {
	t.Helper()
	if !strings.Contains(body, want) {
		t.Fatalf("expected body to contain %q, got %q", want, body)
	}
}

func requireBodyNotContains(t *testing.T, body string, notWant string) {
	t.Helper()
	if strings.Contains(body, notWant) {
		t.Fatalf("expected body not to contain %q, got %q", notWant, body)
	}
}

func requireQueryValue(t *testing.T, loc *url.URL, key, want string) {
	t.Helper()

	got := loc.Query().Get(key)
	if got != want {
		t.Fatalf("unexpected redirect query %q: got=%q want=%q location=%q", key, got, want, loc.String())
	}
}

func uniqueTestSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
