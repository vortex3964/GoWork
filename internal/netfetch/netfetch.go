// Package netfetch provides a deliberately small HTTP fetcher for untrusted
// remote content.
//
// It accepts only HTTP(S), rejects URLs with embedded credentials, caps
// redirects, time and response size, and blocks loopback, private,
// link-local, unspecified and other reserved IPs at dial time. Redirect hops
// are checked again and every DNS answer is validated immediately before the
// connection is made, which closes the usual DNS-rebinding window. Errors
// never include response bodies or Authorization values.
package netfetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	// DefaultTimeout bounds one complete fetch, including redirects and body
	// reading.
	DefaultTimeout = 20 * time.Second

	// DefaultMaxBodyBytes is the hard response-body cap.
	DefaultMaxBodyBytes int64 = 8 << 20 // 8 MiB

	// DefaultMaxRedirects is the number of redirect hops that may be followed.
	DefaultMaxRedirects = 5

	// DefaultUserAgent identifies GoWork to remote servers.
	DefaultUserAgent = "GoWork/1.0 (+https://github.com/vortex3964/GoWork)"
)

var (
	// ErrInvalidURL is returned for malformed or host-less URLs.
	ErrInvalidURL = errors.New("netfetch: invalid URL")

	// ErrUnsupportedScheme is returned for anything other than http or https.
	ErrUnsupportedScheme = errors.New("netfetch: unsupported URL scheme")

	// ErrUserInfo is returned when a URL embeds credentials.
	ErrUserInfo = errors.New("netfetch: URLs with embedded credentials are not allowed")

	// ErrBlockedAddress is returned for loopback, private, link-local,
	// unspecified, multicast and reserved destination addresses.
	ErrBlockedAddress = errors.New("netfetch: destination address is blocked")

	// ErrTooManyRedirects is returned when the redirect budget is exhausted.
	ErrTooManyRedirects = errors.New("netfetch: too many redirects")

	// ErrBodyTooLarge is returned when a response exceeds MaxBodyBytes.
	ErrBodyTooLarge = errors.New("netfetch: response body is too large")
)

// Options configures a Client. Zero values select the documented defaults.
type Options struct {
	Timeout      time.Duration
	MaxBodyBytes int64
	MaxRedirects int
	UserAgent    string
}

// Client is a safe HTTP(S) fetcher. It is safe for concurrent use.
type Client struct {
	httpClient   *http.Client
	maxBodyBytes int64
	maxRedirects int
	userAgent    string
	lookupIP     lookupIPFunc
}

// Response is a successfully fetched (2xx) response. Body contains the exact
// bytes returned by the server.
type Response struct {
	URL        string
	StatusCode int
	Header     http.Header
	Body       []byte
}

// StatusError reports a non-2xx response. It carries the response headers so
// callers can inspect rate-limit metadata, but never carries the body.
type StatusError struct {
	Code   int
	Status string
	Header http.Header
}

func (e *StatusError) Error() string {
	if e == nil {
		return "netfetch: request failed"
	}
	if e.Status != "" {
		return "netfetch: HTTP " + e.Status
	}
	return fmt.Sprintf("netfetch: HTTP status %d", e.Code)
}

// New returns a Client with the supplied options and a guarded transport.
// Proxies from the environment are intentionally ignored: dialing through a
// proxy would move the private-address decision outside this process.
func New(opts Options) *Client {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	maxBody := opts.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = DefaultMaxBodyBytes
	}
	maxRedirects := opts.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = DefaultMaxRedirects
	}
	userAgent := strings.TrimSpace(opts.UserAgent)
	if userAgent == "" {
		userAgent = DefaultUserAgent
	}

	client := &Client{
		maxBodyBytes: maxBody,
		maxRedirects: maxRedirects,
		userAgent:    userAgent,
		lookupIP:     defaultLookupIP,
	}
	client.httpClient = &http.Client{
		Timeout:       timeout,
		Transport:     client.newTransport(timeout),
		CheckRedirect: client.checkRedirect,
	}
	return client
}

// Get fetches rawURL and returns the exact response body.
func (c *Client) Get(ctx context.Context, rawURL string) ([]byte, error) {
	response, err := c.Fetch(ctx, rawURL, nil)
	if err != nil {
		return nil, err
	}
	return response.Body, nil
}

// Fetch performs a GET request and returns the response metadata and body.
// Only 2xx responses succeed. Extra headers are copied onto the request; the
// caller is responsible for only sending credentials to the right host.
func (c *Client) Fetch(ctx context.Context, rawURL string, headers http.Header) (*Response, error) {
	if c == nil || c.httpClient == nil {
		return nil, errors.New("netfetch: client is not initialized")
	}
	if ctx == nil {
		return nil, errors.New("netfetch: nil context")
	}

	trimmed := strings.TrimSpace(rawURL)
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, ErrInvalidURL
	}
	if err := validateURL(parsed); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, ErrInvalidURL
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "*/*")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, sanitizeError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &StatusError{
			Code:   resp.StatusCode,
			Status: resp.Status,
			Header: resp.Header.Clone(),
		}
	}
	if resp.ContentLength > c.maxBodyBytes {
		return nil, fmt.Errorf("%w: limit is %d bytes", ErrBodyTooLarge, c.maxBodyBytes)
	}

	body, err := readLimited(resp.Body, c.maxBodyBytes)
	if err != nil {
		return nil, err
	}

	finalURL := trimmed
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	return &Response{
		URL:        finalURL,
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       body,
	}, nil
}

func (c *Client) newTransport(timeout time.Duration) *http.Transport {
	var transport *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = base.Clone()
	} else {
		transport = &http.Transport{
			TLSHandshakeTimeout:   10 * time.Second,
			IdleConnTimeout:       90 * time.Second,
			ExpectContinueTimeout: time.Second,
			ForceAttemptHTTP2:     true,
		}
	}
	transport.Proxy = nil
	// A custom DialTLS hook would bypass the guarded DialContext, so clear
	// both legacy TLS dial hooks before installing the policy.
	transport.DialTLS = nil
	transport.DialTLSContext = nil
	dialer := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
	}
	transport.DialContext = c.dialContext(dialer)
	return transport
}

func readLimited(reader io.Reader, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, max+1))
	if err != nil {
		return nil, fmt.Errorf("netfetch: read response body: %w", err)
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%w: limit is %d bytes", ErrBodyTooLarge, max)
	}
	return data, nil
}

// sanitizeError keeps sentinel errors and context outcomes inspectable while
// dropping the *url.Error that would otherwise echo request URLs into logs.
func sanitizeError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	case errors.Is(err, ErrInvalidURL):
		return ErrInvalidURL
	case errors.Is(err, ErrUnsupportedScheme):
		return ErrUnsupportedScheme
	case errors.Is(err, ErrUserInfo):
		return ErrUserInfo
	case errors.Is(err, ErrBlockedAddress):
		return ErrBlockedAddress
	case errors.Is(err, ErrTooManyRedirects):
		return ErrTooManyRedirects
	case errors.Is(err, ErrBodyTooLarge):
		return err
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if urlErr.Timeout() {
			return fmt.Errorf("netfetch: request timed out: %w", context.DeadlineExceeded)
		}
		return fmt.Errorf("netfetch: request failed: %w", urlErr.Err)
	}
	return fmt.Errorf("netfetch: request failed: %w", err)
}

// SetGitHubAuthHeader adds a bearer token for GitHub requests. Empty tokens
// and tokens containing CR/LF are ignored, so a value can never inject
// additional headers. The token is not logged anywhere.
func SetGitHubAuthHeader(header http.Header, token string) {
	token = strings.TrimSpace(token)
	if header == nil || token == "" || strings.ContainsAny(token, "\r\n") {
		return
	}
	header.Set("Authorization", "Bearer "+token)
}

// SetGitHubAuth adds the bearer token to req when it is non-empty. The token
// is never written to logs or included in returned errors.
func SetGitHubAuth(req *http.Request, token string) {
	if req == nil {
		return
	}
	SetGitHubAuthHeader(req.Header, token)
}

// GitHubAuthHeader returns a header map containing the GitHub Authorization
// header, or an empty map when no valid token was supplied.
func GitHubAuthHeader(token string) http.Header {
	header := make(http.Header)
	SetGitHubAuthHeader(header, token)
	return header
}

// GitHubTokenFromEnv returns GITHUB_TOKEN, then GH_TOKEN, or "" when neither
// is set. It trims whitespace but never logs the value.
func GitHubTokenFromEnv() string {
	for _, key := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if token := strings.TrimSpace(os.Getenv(key)); token != "" {
			return token
		}
	}
	return ""
}
