package netfetch

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func response(req *http.Request, status int, header http.Header, body []byte) *http.Response {
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		StatusCode:    status,
		Status:        http.StatusText(status),
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

func TestValidateURL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr error
	}{
		{name: "http", raw: "http://example.com/path"},
		{name: "https", raw: "https://example.com/path"},
		{name: "public IPv4", raw: "http://1.1.1.1/"},
		{name: "public IPv6", raw: "http://[2606:4700:4700::1111]/"},
		{name: "uppercase scheme", raw: "HTTPS://example.com/"},
		{name: "ftp", raw: "ftp://example.com/file", wantErr: ErrUnsupportedScheme},
		{name: "file", raw: "file:///etc/passwd", wantErr: ErrUnsupportedScheme},
		{name: "userinfo", raw: "https://user:pass@example.com/", wantErr: ErrUserInfo},
		{name: "username only", raw: "https://user@example.com/", wantErr: ErrUserInfo},
		{name: "localhost", raw: "http://localhost:8080/", wantErr: ErrBlockedAddress},
		{name: "localhost dot", raw: "http://localhost./", wantErr: ErrBlockedAddress},
		{name: "localhost subdomain", raw: "http://foo.localhost/", wantErr: ErrBlockedAddress},
		{name: "loopback IPv4", raw: "http://127.0.0.1/", wantErr: ErrBlockedAddress},
		{name: "loopback IPv6", raw: "http://[::1]/", wantErr: ErrBlockedAddress},
		{name: "private 10", raw: "http://10.2.3.4/", wantErr: ErrBlockedAddress},
		{name: "private 172", raw: "http://172.16.0.1/", wantErr: ErrBlockedAddress},
		{name: "private 192", raw: "http://192.168.1.1/", wantErr: ErrBlockedAddress},
		{name: "link local", raw: "http://169.254.169.254/latest/meta-data", wantErr: ErrBlockedAddress},
		{name: "unspecified", raw: "http://0.0.0.0/", wantErr: ErrBlockedAddress},
		{name: "unspecified IPv6", raw: "http://[::]/", wantErr: ErrBlockedAddress},
		{name: "unique local", raw: "http://[fc00::1]/", wantErr: ErrBlockedAddress},
		{name: "link local IPv6", raw: "http://[fe80::1]/", wantErr: ErrBlockedAddress},
		{name: "CGNAT", raw: "http://100.64.1.1/", wantErr: ErrBlockedAddress},
		{name: "missing host", raw: "https:///path", wantErr: ErrInvalidURL},
		{name: "scheme relative", raw: "//example.com/path", wantErr: ErrUnsupportedScheme},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := url.Parse(tt.raw)
			if err != nil {
				t.Fatalf("parse %q: %v", tt.raw, err)
			}
			err = validateURL(parsed)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("validateURL(%q) = %v, want nil", tt.raw, err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("validateURL(%q) = %v, want %v", tt.raw, err, tt.wantErr)
			}
		})
	}
}

func TestBlockedIPRanges(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.1.2.3", "::1",
		"10.0.0.1", "172.16.0.1", "192.168.0.1",
		"169.254.169.254", "fe80::1",
		"0.0.0.0", "::", "0.1.2.3",
		"100.64.0.1", "192.0.0.1", "198.18.0.1", "240.0.0.1", "255.255.255.255",
		"224.0.0.1", "ff02::1", "fc00::1", "fd12:3456::1",
		"::ffff:127.0.0.1", "::ffff:10.0.0.1",
	}
	for _, raw := range blocked {
		ip := net.ParseIP(raw)
		if ip == nil {
			t.Fatalf("test setup: invalid IP %q", raw)
		}
		if !isBlockedIP(ip) {
			t.Errorf("isBlockedIP(%s) = false, want true", raw)
		}
	}

	allowed := []string{"1.1.1.1", "8.8.8.8", "93.184.216.34", "2606:4700:4700::1111"}
	for _, raw := range allowed {
		ip := net.ParseIP(raw)
		if ip == nil {
			t.Fatalf("test setup: invalid IP %q", raw)
		}
		if isBlockedIP(ip) {
			t.Errorf("isBlockedIP(%s) = true, want false", raw)
		}
	}
	if !isBlockedIP(nil) {
		t.Error("isBlockedIP(nil) = false, want true")
	}
}

func TestDialGuardBlocksEveryResolvedAddress(t *testing.T) {
	client := New(Options{})
	dial := client.dialContext(&net.Dialer{Timeout: time.Second})

	client.lookupIP = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("1.1.1.1"), net.ParseIP("127.0.0.1")}, nil
	}
	_, err := dial(context.Background(), "tcp", "rebind.example:443")
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("dial error = %v, want ErrBlockedAddress", err)
	}

	// A literal private address must be refused without any DNS lookup.
	lookedUp := false
	client.lookupIP = func(context.Context, string) ([]net.IP, error) {
		lookedUp = true
		return nil, errors.New("should not resolve")
	}
	_, err = dial(context.Background(), "tcp", "127.0.0.1:80")
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("literal dial error = %v, want ErrBlockedAddress", err)
	}
	if lookedUp {
		t.Fatal("dial guard resolved a literal private IP instead of rejecting it")
	}
}

func TestFetchReturnsExactBytesAndDefaults(t *testing.T) {
	body := []byte("line one\r\nline two\x00without trailing newline")
	var gotRequest *http.Request
	client := New(Options{})
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotRequest = req
		return response(req, http.StatusOK, http.Header{"Content-Type": {"text/plain"}}, body), nil
	})

	got, err := client.Get(context.Background(), "https://example.com/SKILL.md")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("body = %q, want exact %q", got, body)
	}
	if gotRequest.Header.Get("User-Agent") != DefaultUserAgent {
		t.Fatalf("User-Agent = %q, want %q", gotRequest.Header.Get("User-Agent"), DefaultUserAgent)
	}
	if gotRequest.Header.Get("Accept") != "*/*" {
		t.Fatalf("Accept = %q, want */*", gotRequest.Header.Get("Accept"))
	}
}

func TestFetchRejectsNon2xxWithoutLeakingBody(t *testing.T) {
	token := "ghp_supersecret_token"
	client := New(Options{})
	var sawAuth string
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		sawAuth = req.Header.Get("Authorization")
		header := make(http.Header)
		header.Set("X-RateLimit-Remaining", "0")
		header.Set("X-RateLimit-Reset", "1700000000")
		return response(req, http.StatusForbidden, header, []byte("server body contains "+token)), nil
	})

	_, err := client.Fetch(context.Background(), "https://api.github.com/repos/acme/skills", GitHubAuthHeader(token))
	if err == nil {
		t.Fatal("Fetch succeeded, want status error")
	}
	if sawAuth != "Bearer "+token {
		t.Fatalf("Authorization = %q, want bearer token", sawAuth)
	}
	var status *StatusError
	if !errors.As(err, &status) {
		t.Fatalf("error type = %T, want *StatusError", err)
	}
	if status.Code != http.StatusForbidden {
		t.Fatalf("status code = %d, want 403", status.Code)
	}
	if got := status.Header.Get("X-RateLimit-Remaining"); got != "0" {
		t.Fatalf("status header = %q, want 0", got)
	}
	message := err.Error()
	if strings.Contains(message, token) {
		t.Fatalf("error leaked the token: %q", message)
	}
	if strings.Contains(message, "server body") {
		t.Fatalf("error leaked the response body: %q", message)
	}
	if strings.Contains(message, "api.github.com") {
		t.Fatalf("error leaked the request URL: %q", message)
	}
}

func TestFetchEnforcesBodyLimit(t *testing.T) {
	client := New(Options{MaxBodyBytes: 4})
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return response(req, http.StatusOK, nil, []byte("12345")), nil
	})
	_, err := client.Get(context.Background(), "https://example.com/large")
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("Get error = %v, want ErrBodyTooLarge", err)
	}
}

func TestFetchRejectsUserInfoBeforeDial(t *testing.T) {
	called := false
	client := New(Options{})
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		called = true
		return response(req, http.StatusOK, nil, nil), nil
	})
	_, err := client.Get(context.Background(), "https://user:pass@example.com/file")
	if !errors.Is(err, ErrUserInfo) {
		t.Fatalf("error = %v, want ErrUserInfo", err)
	}
	if called {
		t.Fatal("transport was called for a URL with userinfo")
	}
}

func TestRedirectRejectsPrivateAddress(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	client := New(Options{})
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		requests++
		mu.Unlock()
		return response(req, http.StatusFound, http.Header{
			"Location": {"http://127.0.0.1:8080/secret"},
		}, nil), nil
	})

	_, err := client.Get(context.Background(), "https://example.com/SKILL.md")
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("redirect error = %v, want ErrBlockedAddress", err)
	}
	if strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("redirect error was not sanitized: %q", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 1 {
		t.Fatalf("transport requests = %d, want 1 (redirect must not be followed)", requests)
	}
}

func TestRedirectRejectsHostnameResolvingPrivate(t *testing.T) {
	client := New(Options{})
	client.lookupIP = func(_ context.Context, host string) ([]net.IP, error) {
		if host != "rebind.example" {
			t.Fatalf("lookup host = %q, want rebind.example", host)
		}
		return []net.IP{net.ParseIP("10.0.0.7")}, nil
	}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return response(req, http.StatusFound, http.Header{
			"Location": {"http://rebind.example/secret"},
		}, nil), nil
	})

	_, err := client.Get(context.Background(), "https://example.com/start")
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("redirect error = %v, want ErrBlockedAddress", err)
	}
}

func TestRedirectLimit(t *testing.T) {
	requests := 0
	client := New(Options{MaxRedirects: 2})
	client.lookupIP = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("1.1.1.1")}, nil
	}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return response(req, http.StatusFound, http.Header{
			"Location": {"https://example.com/next"},
		}, nil), nil
	})

	_, err := client.Get(context.Background(), "https://example.com/start")
	if !errors.Is(err, ErrTooManyRedirects) {
		t.Fatalf("error = %v, want ErrTooManyRedirects", err)
	}
	if requests != 3 {
		t.Fatalf("transport requests = %d, want 3 (initial plus two redirects)", requests)
	}
}

func TestRedirectRejectsUnsafeScheme(t *testing.T) {
	client := New(Options{})
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return response(req, http.StatusFound, http.Header{
			"Location": {"file:///etc/passwd"},
		}, nil), nil
	})
	_, err := client.Get(context.Background(), "https://example.com/start")
	if !errors.Is(err, ErrUnsupportedScheme) {
		t.Fatalf("error = %v, want ErrUnsupportedScheme", err)
	}
}

func TestFetchHonorsContext(t *testing.T) {
	client := New(Options{})
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := client.Get(ctx, "https://example.com/slow")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
}

func TestGitHubAuthHelpersNeverExposeToken(t *testing.T) {
	token := "ghp_example"
	header := GitHubAuthHeader("  " + token + "  ")
	if got := header.Get("Authorization"); got != "Bearer "+token {
		t.Fatalf("Authorization = %q, want Bearer token", got)
	}
	if got := GitHubAuthHeader("").Get("Authorization"); got != "" {
		t.Fatalf("empty token produced Authorization %q", got)
	}
	if got := GitHubAuthHeader("bad\r\nX-Injected: yes").Get("Authorization"); got != "" {
		t.Fatalf("CRLF token produced Authorization %q", got)
	}

	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	SetGitHubAuth(req, token)
	if got := req.Header.Get("Authorization"); got != "Bearer "+token {
		t.Fatalf("request Authorization = %q", got)
	}

	t.Setenv("GITHUB_TOKEN", " from-github ")
	t.Setenv("GH_TOKEN", "from-gh")
	if got := GitHubTokenFromEnv(); got != "from-github" {
		t.Fatalf("GitHubTokenFromEnv = %q, want from-github", got)
	}
	t.Setenv("GITHUB_TOKEN", "")
	if got := GitHubTokenFromEnv(); got != "from-gh" {
		t.Fatalf("GitHubTokenFromEnv fallback = %q, want from-gh", got)
	}
}

func TestFetchNilContextIsRejected(t *testing.T) {
	client := New(Options{})
	if _, err := client.Get(nil, "https://example.com/"); err == nil {
		t.Fatal("Get with nil context succeeded")
	}
}
