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
	"net/url"
	"os"
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
	username string
	password string
	client   *http.Client
}

func newTestActor(t *testing.T, name, baseURL, username, password string) *testActor {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar for %s: %v", name, err)
	}

	return &testActor{
		t:        t,
		name:     name,
		baseURL:  baseURL,
		username: username,
		password: password,
		client: &http.Client{
			Jar: jar,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (a *testActor) Login() {
	a.t.Helper()

	resp := a.PostForm("/login", url.Values{
		"username": {a.username},
		"password": {a.password},
	})
	requireRedirectPath(a.t, resp, "/my-hopshare")
}

func (a *testActor) PostForm(path string, form url.Values) *http.Response {
	a.t.Helper()

	resp, err := a.client.PostForm(a.baseURL+path, form)
	if err != nil {
		a.t.Fatalf("%s POST %s failed: %v", a.name, path, err)
	}
	return resp
}

func (a *testActor) PostMultipart(path string, fields map[string]string) *http.Response {
	a.t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := writer.WriteField(k, v); err != nil {
			a.t.Fatalf("%s write multipart field %q failed: %v", a.name, k, err)
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

func requireStatus(t *testing.T, resp *http.Response, wantStatus int) string {
	t.Helper()
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != wantStatus {
		t.Fatalf("expected status=%d got=%d body=%q", wantStatus, resp.StatusCode, string(body))
	}
	return string(body)
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
