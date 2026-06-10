package duckserver_test

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/koron/duckpop/duckserver"
	"github.com/koron/duckpop/internal/assert"
)

const (
	duckDBVersion = "v1.5.3"

	versionQuery = "SELECT version() AS V"
	versionWant  = "V\n" + duckDBVersion + "\n"

	idSyntaxError = "ID syntax error: query ID should starts with \"Q_\"\n"
)

type testServer struct {
	srv    *duckserver.Server
	srvWg  *sync.WaitGroup
	cancel context.CancelFunc
	client *http.Client
	URL    string
}

func (ts *testServer) Client() *http.Client {
	return ts.client
}

func (ts *testServer) Shutdown() {
	ts.cancel()
	ts.srvWg.Wait()
}

func startServer0(t *testing.T) *testServer {
	t.Helper()
	return startServer1(t, nil)
}

type configOption func(c *duckserver.Config) *duckserver.Config

func startServer1(t *testing.T, configOpt configOption) *testServer {
	t.Helper()

	config := duckserver.DefaultConfig()
	config.Address = "127.0.0.1:0"
	config.MaxDB = 4
	config.AccessLogFile = "test.discard"
	config.DBHomeDir = t.TempDir()
	config.DBThreads = 1
	config.DBMemoryLimit = "1GiB"
	config.DBMaxTempDirSize = "2GiB"
	config.DBLockConfig = true

	if configOpt != nil {
		config = *(configOpt(&config))
	}

	srv, err := duckserver.New(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		err := srv.Serve(ctx)
		if err != nil {
			t.Helper()
			t.Errorf("serve terminated with error: %s", err)
		}
		wg.Done()
	}()
	srv.WaitServe()

	t.Cleanup(wg.Wait)
	t.Cleanup(cancel)

	return &testServer{
		srv:    srv,
		srvWg:  &wg,
		cancel: cancel,
		client: &http.Client{
			Transport: &http.Transport{},
		},
		URL: srv.URL,
	}
}

type RequestOption func(*http.Request) *http.Request

func authorizationHeader(value string) RequestOption {
	return func(req *http.Request) *http.Request {
		req.Header.Set("Authorization", value)
		return req
	}
}

func authorizationBasic(name, password string) RequestOption {
	s := base64.StdEncoding.EncodeToString([]byte(name + ":" + password))
	return authorizationHeader("Basic " + s)
}

func authorizationBearer(token string) RequestOption {
	return authorizationHeader("Bearer " + token)
}

func doReq(ts *testServer, req *http.Request, options ...RequestOption) (*http.Response, error) {
	// apply options
	for _, o := range options {
		req = o(req)
	}
	return ts.Client().Do(req)
}

func doGet(ts *testServer, path string, options ...RequestOption) (*http.Response, error) {
	req, err := http.NewRequest("GET", ts.URL+path, nil)
	if err != nil {
		return nil, err
	}
	return doReq(ts, req, options...)
}

func doPost(ts *testServer, path, body string, options ...RequestOption) (*http.Response, error) {
	req, err := http.NewRequest("POST", ts.URL+path, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	return doReq(ts, req, options...)
}

func doDelete(ts *testServer, path string, options ...RequestOption) (*http.Response, error) {
	req, err := http.NewRequest("DELETE", ts.URL+path, nil)
	if err != nil {
		return nil, err
	}
	return doReq(ts, req, options...)
}

func readResponse(r *http.Response, err error) (string, error) {
	return readResponse2(r, err, 200, 299)
}

func readResponse2(r *http.Response, err error, codeBegin, codeEnd int) (string, error) {
	if err != nil {
		return "", fmt.Errorf("http failed: %w", err)
	}
	defer r.Body.Close()
	if r.StatusCode < codeBegin || r.StatusCode > codeEnd {
		b, _ := io.ReadAll(r.Body)
		slog.Warn("request failed", "status", r.StatusCode, "body", string(b))
		return "", fmt.Errorf("request failed: %d (%s) - should be between %d and %d", r.StatusCode, r.Status, codeBegin, codeEnd)
	}
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return "", fmt.Errorf("read body failed: %w", err)
	}
	return string(b), nil
}

func readJSONL[T any](r *http.Response, err error) ([]T, error) {
	if err != nil {
		return nil, fmt.Errorf("http failed: %w", err)
	}
	defer r.Body.Close()
	if r.StatusCode < 200 || r.StatusCode > 299 {
		return nil, fmt.Errorf("request failed: %d (%s)", r.StatusCode, r.Status)
	}
	var list []T
	scanner := bufio.NewScanner(r.Body)
	for scanner.Scan() {
		var v T
		b := scanner.Bytes()
		if len(b) == 0 {
			continue
		}
		err := json.Unmarshal(b, &v)
		if err != nil {
			return nil, fmt.Errorf("unmarshal failed: %w", err)
		}
		list = append(list, v)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanner failed: %w", err)
	}
	return list, nil
}

// testQuery0 checks CSV the response for the query.
func testQuery0(t *testing.T, ts *testServer, query, want string) *responseHeader {
	return testQuery1(t, ts, query, want)
}

type responseHeader struct {
	AuthnID      string
	ConnectionID string
	Duration     string
}

func parseResponseHeader(r *http.Response) *responseHeader {
	return &responseHeader{
		AuthnID:      r.Header.Get(duckserver.AuthnIDHeader),
		ConnectionID: r.Header.Get(duckserver.ConnectionIDHeader),
		Duration:     r.Header.Get(duckserver.DurationHeader),
	}
}

// testQuery1 checks CSV the response for the query, with RequestOptions.
func testQuery1(t *testing.T, ts *testServer, query, want string, options ...RequestOption) *responseHeader {
	t.Helper()
	resp, err := doPost(ts, "/?f=csv", query, options...)
	rh := parseResponseHeader(resp)
	got, err := readResponse(resp, err)
	if err != nil {
		t.Error(err)
		return rh
	}
	assert.Equal(t, want, got)
	return rh
}

func testAuthorizedQuery(t *testing.T, ts *testServer, query, want string, wantAuthID *string, options ...RequestOption) {
	t.Helper()
	resp, err := doPost(ts, "/?f=csv", query, options...)
	got, err := readResponse(resp, err)
	if err != nil {
		t.Errorf("request failed: %s", err)
		return
	}
	assert.Equal(t, want, got)
	if wantAuthID == nil {
		if s, ok := resp.Header[duckserver.AuthnIDHeader]; ok {
			t.Errorf("unexpected authn ID provided: %s", s)
		}
		return
	}
	gotAuthID, ok := resp.Header[duckserver.AuthnIDHeader]
	if !ok || len(gotAuthID) == 0 {
		t.Error("unavailable authnID")
		return
	}
	assert.Equal(t, *wantAuthID, gotAuthID[0])
}

func testUnauthorizedQuery(t *testing.T, ts *testServer, query string, options ...RequestOption) {
	t.Helper()
	resp, err := doPost(ts, "/?f=csv", query, options...)
	got, err := readResponse2(resp, err, 401, 401)
	if err != nil {
		t.Errorf("request failed: %s", err)
		return
	}
	assert.Equal(t, "Unauthorized\n", got)
	if s, ok := resp.Header[duckserver.AuthnIDHeader]; ok {
		t.Errorf("unexpected authn ID provided: %s", s)
	}
}

func testAuthorizedInterruptQuery(t *testing.T, ts *testServer, queryID string, want string, wantAuthID *string, options ...RequestOption) {
	t.Helper()
	resp, err := doDelete(ts, "/status/queries/"+queryID, options...)
	got, err := readResponse2(resp, err, 400, 400)

	if err != nil {
		t.Errorf("request failed: %s", err)
		return
	}
	assert.Equal(t, want, got)

	if wantAuthID == nil {
		if s, ok := resp.Header[duckserver.AuthnIDHeader]; ok {
			t.Errorf("unexpected authn ID provided: %s", s)
		}
		return
	}
	gotAuthID, ok := resp.Header[duckserver.AuthnIDHeader]
	if !ok || len(gotAuthID) == 0 {
		t.Error("unavailable authnID")
		return
	}
	assert.Equal(t, *wantAuthID, gotAuthID[0])
}

func testUnauthorizedInterruptQuery(t *testing.T, ts *testServer, queryID string, options ...RequestOption) {
	t.Helper()
	resp, err := doDelete(ts, "/status/queries/"+queryID, options...)
	got, err := readResponse2(resp, err, 401, 401)
	if err != nil {
		t.Errorf("request failed: %s", err)
		return
	}
	assert.Equal(t, "Unauthorized\n", got)
	if s, ok := resp.Header[duckserver.AuthnIDHeader]; ok {
		t.Errorf("unexpected authn ID provided: %s", s)
	}
}

func closeIdleConnections(t *testing.T, ts *testServer) {
	transport, ok := ts.Client().Transport.(*http.Transport)
	if !ok {
		t.Helper()
		t.Error("canot close connections: not http.Trasport")
		return
	}
	transport.CloseIdleConnections()
}

//////////////////////////////////////////////////////////////////////////////
// Test cases

func TestPing(t *testing.T) {
	ts := startServer0(t)
	got, err := readResponse(doGet(ts, "/ping/"))
	if err != nil {
		t.Error(err)
		return
	}
	assert.Equal(t, "OK\r\n", got)
}

func TestQueryDuckDBVersion(t *testing.T) {
	ts := startServer0(t)
	testQuery0(t, ts, versionQuery, versionWant)
}

type TestConnStatus struct {
	ID      string      `json:"ID"`
	DBStats sql.DBStats `json:"DBStats"`
}

func TestStatusConnections(t *testing.T) {
	ts := startServer0(t)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		testQuery0(t, ts, versionQuery, versionWant)
		time.Sleep(200 * time.Millisecond)
		testQuery0(t, ts, versionQuery, versionWant)
		wg.Done()
	}()
	time.Sleep(100 * time.Millisecond)
	got, err := readJSONL[TestConnStatus](doGet(ts, "/status/connections/"))
	if err != nil {
		t.Error(err)
		return
	}
	assert.Equal(t, []TestConnStatus{
		{DBStats: sql.DBStats{
			OpenConnections: 1,
			InUse:           1,
		}},
	}, got, cmpopts.IgnoreFields(TestConnStatus{}, "ID"))
	wg.Wait()
}

// TestQueryStats contains query statistics.
type TestQueryStats struct {
	ID       string `json:"ID"`
	ConnID   string `json:"ConnID"`
	Query    string `json:"Query"`
	Start    string `json:"Start"`
	Duration string `json:"Duration"`
}

func TestCancelQuery(t *testing.T) {
	ts := startServer0(t)
	t.Run("canceled", func(t *testing.T) {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A slow query, to be interrupted
			r, err := doPost(ts, "/", `SELECT count(md5(i::VARCHAR)) as count_md5 FROM range(0, 100000000, 1) t1(i)`)
			const want = "context canceled\nINTERRUPT Error: Interrupted!\n"
			got, err := readResponse2(r, err, 504, 504)
			if err != nil {
				t.Errorf("slow query failed: %s", err)
			}
			assert.Equal(t, want, got)
		}()
		time.Sleep(100 * time.Millisecond)
		// List executing queries
		queries, err := readJSONL[TestQueryStats](doGet(ts, "/status/queries/"))
		if err != nil {
			t.Error(err)
			return
		}
		// Interrupt (DELETE) a query
		if len(queries) != 1 {
			t.Errorf("unexpected number of queries: %d", len(queries))
			return
		}
		r, err := doDelete(ts, "/status/queries/"+queries[0].ID)
		got, err := readResponse2(r, err, 204, 204)
		if err != nil {
			t.Error(err)
		}
		assert.Equal(t, "", got)
		wg.Wait()
	})
	t.Run("not found", func(t *testing.T) {
		r, err := doDelete(ts, "/status/queries/Q_deadbeaf")
		got, err := readResponse2(r, err, 404, 404)
		if err != nil {
			t.Error(err)
		}
		assert.Equal(t, "Not Found\n", got)
	})
}

func configAuthn(name string, noauthz bool) configOption {
	return func(c *duckserver.Config) *duckserver.Config {
		c.AuthnFile = name
		c.NoAuthz = noauthz
		return c
	}
}

func TestAuthnQuery(t *testing.T) {
	t.Run("authorized", func(t *testing.T) {
		ts := startServer1(t, configAuthn("testdata/authn.json", false))
		testAuthorizedQuery(t, ts, versionQuery, versionWant, new("token1"), authorizationBearer("token-0123456789abcdef"))
		testAuthorizedQuery(t, ts, versionQuery, versionWant, new("token2"), authorizationBearer("foobarbaz"))
		testAuthorizedQuery(t, ts, versionQuery, versionWant, new("user1"), authorizationBasic("user1", "abcd1234"))
		testAuthorizedQuery(t, ts, versionQuery, versionWant, new("user2"), authorizationBasic("user2", "xyz789"))
	})
	t.Run("not authorized", func(t *testing.T) {
		ts := startServer1(t, configAuthn("testdata/authn.json", false))
		testAuthorizedQuery(t, ts, versionQuery, versionWant, new("token1"), authorizationBearer("token-0123456789abcdef"))
		// Should be failed without Authorization header
		testUnauthorizedQuery(t, ts, `SELECT version() AS V`)
		// Should be failed for with wrong Authorization header
		testUnauthorizedQuery(t, ts, `SELECT version() AS V`, authorizationBearer("unknown-token"))
	})
	t.Run("without authorization", func(t *testing.T) {
		ts := startServer1(t, configAuthn("testdata/authn.json", true))
		testAuthorizedQuery(t, ts, versionQuery, versionWant, new("token1"), authorizationBearer("token-0123456789abcdef"))
		testAuthorizedQuery(t, ts, versionQuery, versionWant, nil)
	})
}

func TestAuthnInterruptQuery(t *testing.T) {
	t.Run("authorized", func(t *testing.T) {
		ts := startServer1(t, configAuthn("testdata/authn.json", false))
		testAuthorizedInterruptQuery(t, ts, "dummy", idSyntaxError, new("token1"), authorizationBearer("token-0123456789abcdef"))
		testAuthorizedInterruptQuery(t, ts, "dummy", idSyntaxError, new("token2"), authorizationBearer("foobarbaz"))
		testAuthorizedInterruptQuery(t, ts, "dummy", idSyntaxError, new("user1"), authorizationBasic("user1", "abcd1234"))
		testAuthorizedInterruptQuery(t, ts, "dummy", idSyntaxError, new("user2"), authorizationBasic("user2", "xyz789"))
	})
	t.Run("not authorized", func(t *testing.T) {
		ts := startServer1(t, configAuthn("testdata/authn.json", false))
		testAuthorizedInterruptQuery(t, ts, "dummy", idSyntaxError, new("token1"), authorizationBearer("token-0123456789abcdef"))
		testUnauthorizedInterruptQuery(t, ts, "dummy")
		testUnauthorizedInterruptQuery(t, ts, "dummy", authorizationBearer("unknown-token"))
	})
	t.Run("without authorization", func(t *testing.T) {
		ts := startServer1(t, configAuthn("testdata/authn.json", true))
		testAuthorizedInterruptQuery(t, ts, "dummy", idSyntaxError, new("token1"), authorizationBearer("token-0123456789abcdef"))
		testAuthorizedInterruptQuery(t, ts, "dummy", idSyntaxError, nil)
	})
}

func TestSharedDir(t *testing.T) {
	ts := startServer0(t)
	testQuery0(t, ts, `COPY (SELECT * FROM duckdb_settings() LIMIT 50) TO (public_dir('settings.csv'))`, "Count\n50\n")

	assert.IsRegularFile(t, filepath.Join(ts.srv.SharedDir(), "settings.csv"))
	if t.Failed() {
		return
	}

	closeIdleConnections(t, ts)
	testQuery0(t, ts, `CREATE TABLE shared_settings AS SELECT * FROM read_csv_auto(public_dir('settings.csv'))`, "Count\n50\n")
}

func TestPrivateDir(t *testing.T) {
	ts := startServer0(t)
	privateRoot := ts.srv.PrivateRoot()

	// The contents of the private directory are preserved.
	rh1 := testQuery0(t, ts, `COPY (SELECT * FROM duckdb_settings() LIMIT 50) TO (private_dir('settings.csv'))`, "Count\n50\n")
	assert.IsRegularFile(t, filepath.Join(privateRoot, rh1.ConnectionID, "settings.csv"))
	if t.Failed() {
		return
	}
	testQuery0(t, ts, `CREATE TABLE shared_settings AS SELECT * FROM read_csv_auto(private_dir('settings.csv'))`, "Count\n50\n")

	// Verify that the private directory is gone after disconnected.
	closeIdleConnections(t, ts)
	time.Sleep(100 * time.Millisecond)
	assert.IsNotExist(t, filepath.Join(privateRoot, rh1.ConnectionID))
}

func TestReadQuery(t *testing.T) {
	ts := startServer0(t)

	t.Run("q", func(t *testing.T) {
		got, err := readResponse(doGet(ts, "/?f=csv&q="+url.QueryEscape(versionQuery)))
		if err != nil {
			t.Error(err)
		}
		assert.Equal(t, versionWant, got)
	})

	t.Run("query", func(t *testing.T) {
		got, err := readResponse(doGet(ts, "/?f=csv&query="+url.QueryEscape(versionQuery)))
		if err != nil {
			t.Error(err)
		}
		assert.Equal(t, versionWant, got)
	})

	t.Run("no_queries", func(t *testing.T) {
		resp, err := doGet(ts, "/?f=csv")
		got, err := readResponse2(resp, err, 400, 400)
		if err != nil {
			t.Error(err)
		}
		assert.Equal(t, "No queries: no queries\n", got)
	})
}

func TestPIDFile(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "test.pid")
	ts := startServer1(t, func(c *duckserver.Config) *duckserver.Config {
		c.PIDFile = pidfile
		return c
	})
	assert.IsRegularFile(t, pidfile)
	ts.Shutdown()
	assert.IsNotExist(t, pidfile)
}

func TestGetConfigJSON(t *testing.T) {
	var homedir string
	ts := startServer1(t, func(c *duckserver.Config) *duckserver.Config {
		homedir = c.DBHomeDir
		return c
	})
	got, err := readResponse(doGet(ts, "/config/"))
	if err != nil {
		t.Error(err)
	}
	want := `{
  "EnableDebugLog": false,
  "EnablePprof": false,
  "Address": "127.0.0.1:0",
  "MaxDB": 4,
  "PIDFile": "",
  "AccessLogFile": "test.discard",
  "AccessLogFormat": "text",
  "AuthnFile": "",
  "NoAuthz": false,
  "DBHomeDir": ` + strconv.Quote(homedir) + `,
  "DBThreads": 1,
  "DBMemoryLimit": "1GiB",
  "DBMaxTempDirSize": "2GiB",
  "DBExternalAccess": true,
  "DBLockConfig": true,
  "DBInitQuery": "",
  "UIResourceFS": null
}
`
	assert.Equal(t, want, got)
}

func TestRedirectToUI(t *testing.T) {
	t.Run("normal", func(t *testing.T) {
		ts := startServer1(t, func(c *duckserver.Config) *duckserver.Config {
			c.UIResourceFS = os.DirFS("./testdata/ui")
			return c
		})
		got, err := readResponse(doGet(ts, "/"))
		if err != nil {
			t.Error(err)
			return
		}
		assert.Equal(t, "<h1>Test UI</h1>\n", got)
	})

	t.Run("authn", func(t *testing.T) {
		// Even if authentication is enabled but authentication credentials are
		// not provided, redirect to the UI.
		ts := startServer1(t, func(c *duckserver.Config) *duckserver.Config {
			c.UIResourceFS = os.DirFS("testdata/ui")
			c.AuthnFile = "testdata/authn.json"
			return c
		})
		got, err := readResponse(doGet(ts, "/"))
		if err != nil {
			t.Error(err)
			return
		}
		assert.Equal(t, "<h1>Test UI</h1>\n", got)
	})
}
