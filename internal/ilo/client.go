package ilo

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
)

type Client struct {
	base       *url.URL
	http       *http.Client
	sessionKey string
}

type Options struct {
	Addr       string
	VerifyCert bool
}

type SessionInfo struct {
	Key        string `json:"key"`
	SessionKey string `json:"session_key"`
}

func NewClient(opts Options) (*Client, error) {
	host, port, err := ParseAddress(opts.Addr)
	if err != nil {
		return nil, err
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	base, err := url.Parse("https://" + net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		return nil, err
	}
	return &Client{
		base: base,
		http: &http.Client{
			Jar: jar,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: !opts.VerifyCert}, //nolint:gosec // iLO commonly uses self-signed certs.
			},
		},
	}, nil
}

func (c *Client) Login(ctx context.Context, user, password string) (*SessionInfo, error) {
	body := map[string]string{
		"method":     "login",
		"user_login": user,
		"password":   password,
	}
	var session SessionInfo
	if err := c.postJSON(ctx, "/json/login_session", body, &session); err != nil {
		return nil, err
	}
	key := session.Key
	if key == "" {
		key = session.SessionKey
	}
	if key == "" {
		return nil, fmt.Errorf("iLO login did not return a session key")
	}
	session.Key = key
	c.sessionKey = key
	return &session, nil
}

func (c *Client) Logout(ctx context.Context) error {
	if c.sessionKey == "" {
		return nil
	}
	body := map[string]string{
		"method":      "logout",
		"session_key": c.sessionKey,
	}
	var ignored map[string]any
	err := c.postJSON(ctx, "/json/login_session", body, &ignored)
	c.sessionKey = ""
	return err
}

func (c *Client) SessionKey() string {
	return c.sessionKey
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	return c.requestJSON(ctx, http.MethodGet, path, nil, out)
}

func (c *Client) postJSON(ctx context.Context, path string, in, out any) error {
	return c.requestJSON(ctx, http.MethodPost, path, in, out)
}

func (c *Client) patchJSON(ctx context.Context, path string, in, out any) error {
	return c.requestJSON(ctx, http.MethodPatch, path, in, out)
}

func (c *Client) requestJSON(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path), body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.doJSON(req, out)
}

func (c *Client) doJSON(req *http.Request, out any) error {
	if c.sessionKey != "" {
		req.Header.Set("X-Auth-Token", c.sessionKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return HTTPError{StatusCode: resp.StatusCode, Body: string(b)}
	}
	if out == nil || len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("%s: %w", req.URL.Path, err)
	}
	return nil
}

func (c *Client) url(path string) string {
	u := *c.base
	u.Path = path
	return u.String()
}

type HTTPError struct {
	StatusCode int
	Body       string
}

func (e HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body)
}
