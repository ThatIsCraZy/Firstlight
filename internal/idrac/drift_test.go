package idrac

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The controller serves its own web interface as readable source. Two of those
// files carry what this package needs to stay in step with it: a flat table of
// endpoint constants, and the factory that performs the login. The tests below
// read them and compare them with what this package sends. Firmware 7.30 moved
// the login from two custom headers to HTTP basic authentication, and the only
// symptom in the field was HTTP 400 with valid credentials. A check like this
// names the change instead of leaving it to be discovered.
//
// Nothing read here reaches the client at run time, and that is deliberate. An
// unauthenticated controller behind a self-signed certificate is not a place to
// take instructions from; it is a place to compare notes with. These are test
// files, so they are not part of the shipped binary at all: a drifting
// controller fails a test, it can never steer or block a session.
//
// The files are found by following the login page rather than by their paths,
// because a renamed file is not drift. When they cannot be found or cannot be
// parsed, the tests skip and say so. A check that has gone stale must not look
// like a broken client.

const maxVendorSource = 4 << 20

func driftAddr(t *testing.T) string {
	t.Helper()
	addr := os.Getenv("FIRSTLIGHT_IDRAC_ADDR")
	if addr == "" {
		t.Skip("set FIRSTLIGHT_IDRAC_ADDR to compare against a controller; no credentials are needed")
	}
	return addr
}

func driftClient(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // iDRACs ship self-signed certificates.
		},
	}
}

// fetch returns the body and the URL it came from, following redirects. Files
// below the web interface are served only to requests that advertise gzip,
// which the Go transport does on its own.
func fetch(t *testing.T, client *http.Client, target string) (string, *url.URL, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("request %s: %v", target, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Skipf("the controller did not serve %s: %v", target, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxVendorSource))
	if err != nil {
		t.Skipf("reading %s: %v", target, err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", resp.Request.URL, false
	}
	return string(body), resp.Request.URL, true
}

var (
	scriptSrc      = regexp.MustCompile(`(?i)<script[^>]*\ssrc\s*=\s*["']([^"']+)["']`)
	vendorConstant = regexp.MustCompile(`(?m)^\s*(?:var\s+)?([A-Z0-9_]+)\s*=\s*"([^"]*)"`)
	headerName     = regexp.MustCompile(`['"]([A-Za-z][A-Za-z0-9-]*)['"]\s*:`)
	libraryPath    = regexp.MustCompile(`/(angular|bootstrap|chart|lib|css|images)/`)
)

// vendorSources follows the login page to whatever scripts it loads now and
// returns the two this check cares about. Renaming or moving a file does not
// break it; only the pair disappearing does, and that is reported as a skip.
func vendorSources(t *testing.T, addr string) (uriTable, loginSource string) {
	t.Helper()
	client := driftClient(t)
	base, err := NewClient(Options{Addr: addr})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	// The root redirects to the login page, whatever it is called this time.
	page, pageURL, ok := fetch(t, client, base.url("/"))
	if !ok {
		t.Skip("the controller did not serve a login page to compare against")
	}
	t.Logf("login page: %s", pageURL.Path)

	for _, match := range scriptSrc.FindAllStringSubmatch(page, -1) {
		src := match[1]
		if libraryPath.MatchString("/"+src) || strings.Contains(src, ".min.") {
			continue
		}
		ref, err := pageURL.Parse(src)
		if err != nil {
			continue
		}
		body, _, ok := fetch(t, client, ref.String())
		if !ok {
			continue
		}
		// Classify by content, not by name.
		if uriTable == "" && strings.Contains(body, `= "/sysmgmt/`) {
			uriTable = body
			t.Logf("endpoint table: %s", ref.Path)
		}
		if loginSource == "" && strings.Contains(body, "loginservice") && strings.Contains(body, "login:") {
			loginSource = body
			t.Logf("login source: %s", ref.Path)
		}
	}
	return uriTable, loginSource
}

func TestLiveVendorEndpointsStillMatchOurs(t *testing.T) {
	uriTable, _ := vendorSources(t, driftAddr(t))
	if uriTable == "" {
		t.Skip("no endpoint table found on this controller; the check needs a new anchor, which says nothing about the client")
	}
	found := map[string]string{}
	for _, match := range vendorConstant.FindAllStringSubmatch(uriTable, -1) {
		found[match[1]] = match[2]
	}
	t.Logf("%d endpoint constants read from the controller", len(found))

	compared := 0
	for _, want := range []struct {
		constant string
		ours     string
		what     string
	}{
		{"LOGIN_URI", sessionPath, "web session"},
		{"LAUNCH_CONSOLE_URI", consolePath, "console ticket"},
		{"LAUNCH_VMEDIA_URI", vmediaPath, "virtual media ticket"},
		{"CONSOLE_PREVIEW_URI", previewPath, "console preview image"},
	} {
		theirs, ok := found[want.constant]
		if !ok {
			// The constant was renamed, which this check cannot follow and
			// which on its own tells us nothing about the endpoint.
			t.Logf("no %s in the controller's table; the %s could not be compared", want.constant, want.what)
			continue
		}
		compared++
		if theirs != want.ours {
			t.Errorf("%s: the controller now uses %q for the %s, this package sends %q",
				want.constant, theirs, want.what, want.ours)
		}
	}
	if compared == 0 {
		t.Skip("none of the four constants could be located; the check is stale, not the client")
	}
	t.Logf("%d of 4 endpoints compared", compared)
}

// loginCall returns the body of the controller's own login call and the header
// names it sets.
func loginCall(source string) (body string, headers []string, ok bool) {
	start := strings.Index(source, "loginservice")
	if start < 0 {
		return "", nil, false
	}
	body = source[start:]
	begin := strings.Index(body, "login:")
	if begin < 0 {
		return "", nil, false
	}
	body = body[begin:]
	// The next entry of the factory ends the call this check cares about.
	for _, next := range []string{"sclogin:", "sso:", "verifyCode:"} {
		if end := strings.Index(body, next); end > 0 && end < len(body) {
			body = body[:end]
		}
	}
	block := ""
	if at := strings.Index(body, "headers:"); at >= 0 {
		block = body[at:]
		if end := strings.Index(block, "}"); end > 0 {
			block = block[:end]
		}
	}
	for _, match := range headerName.FindAllStringSubmatch(block, -1) {
		headers = append(headers, match[1])
	}
	return body, headers, true
}

func TestLiveVendorLoginStillUsesTheCredentialFormWeSend(t *testing.T) {
	_, loginSource := vendorSources(t, driftAddr(t))
	if loginSource == "" {
		t.Skip("no login source found on this controller; the check needs a new anchor, which says nothing about the client")
	}
	body, headers, ok := loginCall(loginSource)
	if !ok {
		t.Skip("the login call could not be located in the controller's source; the check is stale, not the client")
	}
	t.Logf("the controller's own login sets these headers: %v", headers)

	// Everything this package puts on a login request. A header the controller
	// sets that is not in here is worth a look, because it is the shape the
	// 7.30 change took.
	weSend := map[string]bool{"Authorization": true, "user": true, "password": true}
	for _, name := range headers {
		if !weSend[name] {
			t.Errorf("the controller's login now sets a %q header this package does not send; check whether the firmware requires it", name)
		}
	}

	basic := strings.Contains(body, "Authorization") && strings.Contains(body, "btoa")
	legacy := false
	for _, name := range headers {
		if name == "user" || name == "password" {
			legacy = true
		}
	}
	switch {
	case basic:
		t.Log("credential form: HTTP basic authentication, which this package sends")
	case legacy:
		t.Log("credential form: the custom user and password headers, which this package also sends")
	default:
		t.Errorf("the controller's login uses neither basic authentication nor the custom headers: %s",
			strings.Join(strings.Fields(body), " "))
	}
}

// The two tests below need no controller. They prove the check follows content
// rather than file names, which is the difference between reporting drift and
// reporting a rename.

func renamedController(t *testing.T) string {
	t.Helper()
	// Everything is where a future firmware might move it: a different login
	// page under a different directory, loading scripts with hashed names.
	const uriTable = `
	var BMC_INFO = "/sysmgmt/2015/bmc/info"
	var LOGIN_URI = "/sysmgmt/2015/bmc/session"
	var LAUNCH_CONSOLE_URI = "/sysmgmt/2015/server/vconsole"
	var LAUNCH_VMEDIA_URI = "/sysmgmt/2018/server/vmedia"
	var CONSOLE_PREVIEW_URI = "/capconsole/scapture0.png"
	`
	const loginSource = `
	.factory('loginservice', ['$http', function($http) {
	  return {
	    login: function(username, password) {
	      return $http({ method: "POST", url: LOGIN_URI,
	        headers: { 'Authorization': 'Basic ' + btoa(username + ':' + password) } });
	    },
	    sclogin: function(data) { return $http({ method: "POST", url: LOGIN_URI, data: data }); }
	  };
	}])
	`
	const page = `<html><body>
	<script src="lib/angular/angular.min.js"></script>
	<script src="scripts/a1b2c3d4.js"></script>
	<script src="scripts/e5f6a7b8.js"></script>
	</body></html>`

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/v2/entry.html", http.StatusFound)
	})
	mux.HandleFunc("/ui/v2/entry.html", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, page)
	})
	mux.HandleFunc("/ui/v2/scripts/a1b2c3d4.js", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, uriTable)
	})
	mux.HandleFunc("/ui/v2/scripts/e5f6a7b8.js", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, loginSource)
	})
	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	return strings.TrimPrefix(server.URL, "https://")
}

func TestDriftCheckFollowsRenamedFiles(t *testing.T) {
	uriTable, loginSource := vendorSources(t, renamedController(t))
	if uriTable == "" {
		t.Fatal("the endpoint table was not found under its new name")
	}
	if loginSource == "" {
		t.Fatal("the login source was not found under its new name")
	}

	found := map[string]string{}
	for _, match := range vendorConstant.FindAllStringSubmatch(uriTable, -1) {
		found[match[1]] = match[2]
	}
	if got := found["LOGIN_URI"]; got != sessionPath {
		t.Fatalf("LOGIN_URI = %q, want %q", got, sessionPath)
	}
	_, headers, ok := loginCall(loginSource)
	if !ok || len(headers) != 1 || headers[0] != "Authorization" {
		t.Fatalf("login headers = %v (parsed=%v)", headers, ok)
	}
}

func TestLoginCallReadsTheCredentialForm(t *testing.T) {
	legacy := `.factory('loginservice', [function() { return { login: function(u, p) {
	  return $http({ url: LOGIN_URI, headers: { 'user': '"'+u+'"', 'password': '"'+p+'"' } });
	}, sclogin: function() {} }; }])`
	_, headers, ok := loginCall(legacy)
	if !ok {
		t.Fatal("the older credential form was not recognised")
	}
	if len(headers) != 2 || headers[0] != "user" || headers[1] != "password" {
		t.Fatalf("headers = %v", headers)
	}
	if _, _, ok := loginCall("nothing of interest here"); ok {
		t.Fatal("a file without a login call was reported as one")
	}
}
