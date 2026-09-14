// Package idrac speaks to Dell Integrated Dell Remote Access Controller
// firmware: the web session that issues console tickets, the Redfish endpoints
// that report power and boot state, and the WebSocket channels that carry the
// remote console and virtual media.
//
// Firstlight is an independent project and is not affiliated with Dell.
package idrac

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

const (
	sessionPath = "/sysmgmt/2015/bmc/session"
	consolePath = "/sysmgmt/2015/server/vconsole"
	vmediaPath  = "/sysmgmt/2018/server/vmedia"
	previewPath = "/capconsole/scapture0.png"
	systemPath  = "/redfish/v1/Systems/System.Embedded.1"
	managerPath = "/redfish/v1/Managers/iDRAC.Embedded.1"
	attributes  = "/redfish/v1/Managers/iDRAC.Embedded.1/Oem/Dell/DellAttributes/iDRAC.Embedded.1"
	resetAction = "/redfish/v1/Systems/System.Embedded.1/Actions/ComputerSystem.Reset"
	// virtualMediaCDPath reports the virtual optical drive independently of the
	// media channel.
	virtualMediaCDPath = "/redfish/v1/Managers/iDRAC.Embedded.1/VirtualMedia/CD"
	sessionsPath       = "/redfish/v1/SessionService/Sessions"
	defaultPort        = 443
	maxBodyBytes       = 8 << 20
)

// ErrConsoleDisabled reports that Virtual Console is switched off or the
// licence does not cover it.
var ErrConsoleDisabled = errors.New("virtual console is disabled on this iDRAC")

// Options configures NewClient.
type Options struct {
	// Addr is a host name or address, optionally with a port.
	Addr string
	// VerifyCert turns on certificate verification. iDRACs ship with a
	// self-signed certificate, so callers usually leave this off.
	VerifyCert bool
}

// Client holds one authenticated web session against an iDRAC.
type Client struct {
	base *url.URL
	host string
	http *http.Client

	mu    sync.Mutex
	xsrf  string
	token string
}

// NewClient prepares a client. No network traffic happens until Login.
func NewClient(opts Options) (*Client, error) {
	host, port, err := ParseAddress(opts.Addr)
	if err != nil {
		return nil, err
	}
	base, err := url.Parse("https://" + net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		return nil, err
	}
	return &Client{
		base: base,
		host: host,
		http: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: !opts.VerifyCert}, //nolint:gosec // iDRACs ship self-signed certificates.
			},
		},
	}, nil
}

// ParseAddress splits an address into host and port, defaulting to 443.
func ParseAddress(addr string) (string, uint16, error) {
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" {
		return "", 0, errors.New("iDRAC address is required")
	}
	trimmed = strings.TrimPrefix(strings.TrimPrefix(trimmed, "https://"), "http://")
	trimmed = strings.TrimSuffix(trimmed, "/")
	host, portText, err := net.SplitHostPort(trimmed)
	if err != nil {
		// No port, or an IPv6 literal without brackets.
		return strings.Trim(trimmed, "[]"), defaultPort, nil
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return "", 0, fmt.Errorf("invalid port %q in iDRAC address", portText)
	}
	return host, uint16(port), nil
}

// Host reports the host part of the configured address.
func (c *Client) Host() string { return c.host }

// BaseURL reports the https origin used for requests.
func (c *Client) BaseURL() string { return c.base.String() }

// Login opens a web session. The iDRAC answers with an XSRF token and a
// session cookie; both are needed for the console ticket.
func (c *Client) Login(ctx context.Context, user, password string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url(sessionPath), nil)
	if err != nil {
		return err
	}
	// The firmware expects the credentials quoted inside the header value.
	req.Header.Set("user", strconv.Quote(user))
	req.Header.Set("password", strconv.Quote(password))
	resp, body, err := c.do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("iDRAC login failed: HTTP %d %s", resp.StatusCode, summary(body))
	}
	var result struct {
		AuthResult int `json:"authResult"`
	}
	if err := json.Unmarshal(body, &result); err == nil && result.AuthResult != 0 {
		return fmt.Errorf("iDRAC login rejected, authResult %d", result.AuthResult)
	}
	token := resp.Header.Get("XSRF-TOKEN")
	if token == "" {
		return errors.New("iDRAC login did not return an XSRF token")
	}
	var cookie string
	for _, item := range resp.Cookies() {
		if cookie != "" {
			cookie += "; "
		}
		cookie += item.Name + "=" + item.Value
	}
	if cookie == "" {
		return errors.New("iDRAC login did not set a session cookie")
	}
	c.mu.Lock()
	c.xsrf = token
	c.token = cookie
	c.mu.Unlock()
	return nil
}

// Logout closes the web session. Leaving sessions open exhausts the small
// pool an iDRAC keeps and eventually locks out new logins.
func (c *Client) Logout(ctx context.Context) error {
	c.mu.Lock()
	empty := c.xsrf == ""
	c.mu.Unlock()
	if empty {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.url(sessionPath), nil)
	if err != nil {
		return err
	}
	_, _, err = c.do(req)
	c.mu.Lock()
	c.xsrf, c.token = "", ""
	c.mu.Unlock()
	return err
}

// LoggedIn reports whether a session is open.
func (c *Client) LoggedIn() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.xsrf != ""
}

// credentials returns the session cookie and XSRF token.
func (c *Client) credentials() (cookie, xsrf string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token, c.xsrf
}

// Ticket is a one-shot console credential pair.
type Ticket struct {
	// Key1 authenticates the WebSocket, Key2 answers the firmware's challenge
	// once the channel is open.
	Key1 string
	Key2 string
	// Port is where the console WebSocket listens, normally the web port.
	Port uint16
	// Title is the window caption the web interface would use.
	Title string
}

// ConsoleTicket asks for a fresh console ticket. Each call returns a new pair
// and a ticket can be redeemed only once.
func (c *Client) ConsoleTicket(ctx context.Context) (Ticket, error) {
	return c.ticket(ctx, consolePath)
}

// VirtualMediaTicket asks for a ticket for the standalone virtual media
// channel.
func (c *Client) VirtualMediaTicket(ctx context.Context) (Ticket, error) {
	return c.ticket(ctx, vmediaPath)
}

func (c *Client) ticket(ctx context.Context, path string) (Ticket, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url(path), nil)
	if err != nil {
		return Ticket{}, err
	}
	resp, body, err := c.do(req)
	if err != nil {
		return Ticket{}, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return Ticket{}, errors.New("iDRAC session expired before the console could start")
	}
	if resp.StatusCode/100 != 2 {
		return Ticket{}, fmt.Errorf("console ticket failed: HTTP %d %s", resp.StatusCode, summary(body))
	}
	var result struct {
		Location string `json:"Location"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return Ticket{}, fmt.Errorf("console ticket response: %w", err)
	}
	return parseTicket(result.Location)
}

func parseTicket(location string) (Ticket, error) {
	if strings.TrimSpace(location) == "" {
		return Ticket{}, ErrConsoleDisabled
	}
	parsed, err := url.Parse(location)
	if err != nil {
		return Ticket{}, fmt.Errorf("console ticket location: %w", err)
	}
	query := parsed.Query()
	ticket := Ticket{
		Key1:  query.Get("VCSID"),
		Key2:  query.Get("VCSID2"),
		Title: query.Get("title"),
		Port:  defaultPort,
	}
	if ticket.Key1 == "" {
		return Ticket{}, errors.New("console ticket did not contain a session key")
	}
	if raw := query.Get("kvmport"); raw != "" {
		if port, err := strconv.ParseUint(raw, 10, 16); err == nil && port != 0 {
			ticket.Port = uint16(port)
		}
	}
	return ticket, nil
}

// url builds an absolute request URL. A query string may be appended to the
// path; it is moved into the query component so it does not end up percent
// encoded inside the path.
func (c *Client) url(path string) string {
	u := *c.base
	if index := strings.IndexByte(path, '?'); index >= 0 {
		u.Path, u.RawQuery = path[:index], path[index+1:]
	} else {
		u.Path = path
	}
	return u.String()
}

// do sends a request with the session headers attached and reads the body.
func (c *Client) do(req *http.Request) (*http.Response, []byte, error) {
	cookie, xsrf := c.credentials()
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if xsrf != "" {
		req.Header.Set("XSRF-TOKEN", xsrf)
		// Redfish paths read the same value from a different header.
		req.Header.Set("X-AUTH-TOKEN", xsrf)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json, text/plain, */*")
	}
	// Accept-Encoding stays unset on purpose: the transport then negotiates
	// gzip itself and decompresses transparently, which it will not do for a
	// header the caller set. Only the static files below /restgui require an
	// explicit gzip offer, and this client never fetches those.
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, nil, err
	}
	return resp, body, nil
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url(path), nil)
	if err != nil {
		return err
	}
	resp, body, err := c.do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("GET %s: HTTP %d %s", path, resp.StatusCode, summary(body))
	}
	if out == nil || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

func (c *Client) sendJSON(ctx context.Context, method, path string, in any) error {
	var reader io.Reader
	if in != nil {
		encoded, err := json.Marshal(in)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path), reader)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, body, err := c.do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s: HTTP %d %s", method, path, resp.StatusCode, summary(body))
	}
	return nil
}

// summary shortens a response body for an error message.
func summary(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 200 {
		return text[:200] + "..."
	}
	return text
}
