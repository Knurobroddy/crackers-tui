package remote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Knurobroddy/crackers-tui/internal/config"
	"github.com/Knurobroddy/crackers-tui/internal/logx"
)

// maxJSONSize caps JSON documents; they are small catalogs.
const maxJSONSize = 32 << 20

// ProgressFunc receives bytes done and the expected total (0 if unknown).
type ProgressFunc func(done, total int64)

// Client talks to the remote library.
type Client struct {
	base       *url.URL // nil for a downloader: absolute URLs only
	http       *http.Client
	appVersion string
	log        *slog.Logger
	retryDelay time.Duration // first retry backoff; 1 s when zero (tests shorten it)
}

// NewClient returns a client for the given base URL. The base is treated as a
// directory: a trailing slash is added if missing.
func NewClient(base, appVersion string, log *slog.Logger) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return nil, fmt.Errorf("invalid remote URL %q: %w", base, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("invalid remote URL %q: must be http(s)", base)
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	c := NewDownloader(appVersion, log)
	c.base = u
	return c, nil
}

// NewDownloader returns a client without a base URL, for downloading
// absolute URLs only (e.g. by pack authoring tools).
func NewDownloader(appVersion string, log *slog.Logger) *Client {
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &Client{
		http:       &http.Client{Transport: transport}, // no overall timeout: large downloads
		appVersion: appVersion,
		log:        logx.OrDiscard(log),
	}
}

// Resolve resolves ref (absolute or relative to the base) to an http(s) URL.
func (c *Client) Resolve(ref string) (string, error) {
	r, err := url.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("invalid URL %q: %w", ref, err)
	}
	u := r
	if c.base != nil {
		u = c.base.ResolveReference(r)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("unsupported URL %q: must be an absolute http(s) URL", ref)
	}
	return u.String(), nil
}

// get performs a GET and returns the response if the status is 200.
// Transient failures (network errors, HTTP 429 and 5xx) are retried with
// backoff; hosts such as Thunderstore occasionally answer a burst of
// downloads with a 500.
func (c *Client) get(ctx context.Context, rawURL string) (*http.Response, error) {
	delay := c.retryDelay
	if delay == 0 {
		delay = time.Second
	}
	for attempt := 1; ; attempt++ {
		resp, wait, err := c.getOnce(ctx, rawURL)
		if err == nil || wait < 0 || attempt == maxAttempts {
			return resp, err
		}
		if wait == 0 {
			wait = delay
		}
		c.log.Warn("download failed, retrying", "url", rawURL, "attempt", attempt, "in", wait, "err", err)
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(wait):
		}
		delay *= 2
	}
}

const maxAttempts = 4

// getOnce performs one GET. wait < 0 means the error is permanent; wait > 0
// is a server-requested delay (Retry-After); 0 means use the default backoff.
func (c *Client) getOnce(ctx context.Context, rawURL string) (resp *http.Response, wait time.Duration, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, -1, err
	}
	req.Header.Set("User-Agent", config.UserAgent(c.appVersion))
	resp, err = c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, -1, fmt.Errorf("could not download %s: %w", rawURL, err)
		}
		return nil, 0, fmt.Errorf("could not download %s: %w", rawURL, err)
	}
	if resp.StatusCode == http.StatusOK {
		return resp, 0, nil
	}
	resp.Body.Close()
	err = fmt.Errorf("could not download %s: HTTP %s", rawURL, resp.Status)
	if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
		return nil, -1, err
	}
	if s, perr := strconv.Atoi(resp.Header.Get("Retry-After")); perr == nil && s > 0 {
		wait = min(time.Duration(s)*time.Second, 30*time.Second)
	}
	return nil, wait, err
}

// fetchRaw downloads a JSON document and returns its bytes.
func (c *Client) fetchRaw(ctx context.Context, ref string) ([]byte, string, error) {
	u, err := c.Resolve(ref)
	if err != nil {
		return nil, "", err
	}
	c.log.Info("fetching", "url", u)
	resp, err := c.get(ctx, u)
	if err != nil {
		return nil, u, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxJSONSize+1))
	if err != nil {
		return nil, u, fmt.Errorf("could not download %s: %w", u, err)
	}
	if len(b) > maxJSONSize {
		return nil, u, fmt.Errorf("%s is too large", u)
	}
	return b, u, nil
}

// FetchGames downloads and checks games.json.
func (c *Client) FetchGames(ctx context.Context) (*Games, error) {
	b, u, err := c.fetchRaw(ctx, "games.json")
	if err != nil {
		return nil, err
	}
	var g Games
	if err := json.Unmarshal(b, &g); err != nil {
		return nil, fmt.Errorf("invalid games.json at %s: %w", u, err)
	}
	if err := CheckSchema("games.json", g.SchemaVersion, config.GamesSchemaVersion); err != nil {
		return nil, err
	}
	if err := CheckMinAppVersion("games.json", g.MinAppVersion, c.appVersion); err != nil {
		return nil, err
	}
	return &g, nil
}

// FetchIndex downloads and checks index.json.
func (c *Client) FetchIndex(ctx context.Context) (*Index, error) {
	b, u, err := c.fetchRaw(ctx, "index.json")
	if err != nil {
		return nil, err
	}
	var ix Index
	if err := json.Unmarshal(b, &ix); err != nil {
		return nil, fmt.Errorf("invalid index.json at %s: %w", u, err)
	}
	if err := CheckSchema("index.json", ix.SchemaVersion, config.IndexSchemaVersion); err != nil {
		return nil, err
	}
	if err := CheckMinAppVersion("index.json", ix.MinAppVersion, c.appVersion); err != nil {
		return nil, err
	}
	return &ix, nil
}

// FetchManifest downloads a pack manifest and returns it with the hex SHA-256
// of its raw bytes. The manifest is decoded and validated (Manifest.Validate).
func (c *Client) FetchManifest(ctx context.Context, ref string) (*Manifest, string, error) {
	b, u, err := c.fetchRaw(ctx, ref)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(b)
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, "", fmt.Errorf("invalid pack manifest at %s: %w", u, err)
	}
	if err := m.Validate(); err != nil {
		return nil, "", err
	}
	return &m, hex.EncodeToString(sum[:]), nil
}

// ManifestHash downloads a manifest and returns only the hash of its raw bytes.
func (c *Client) ManifestHash(ctx context.Context, ref string) (string, error) {
	b, _, err := c.fetchRaw(ctx, ref)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// Download streams fe to dst while hashing, then verifies sha256 and size (if
// > 0). On any error dst is removed.
func (c *Client) Download(ctx context.Context, fe FileEntry, dst string, progress ProgressFunc) (err error) {
	u, err := c.Resolve(fe.URL)
	if err != nil {
		return err
	}
	got, n, err := c.download(ctx, u, dst, fe.Size, false, progress)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(dst)
		}
	}()
	if fe.Size > 0 && n != fe.Size {
		return fmt.Errorf("size mismatch for %s: expected %d bytes, got %d", u, fe.Size, n)
	}
	if !strings.EqualFold(got, fe.SHA256) {
		return fmt.Errorf("checksum mismatch for %s: expected sha256 %s, got %s", u, strings.ToLower(fe.SHA256), got)
	}
	c.log.Info("downloaded and verified", "url", u, "bytes", n)
	return nil
}

// Fetch downloads ref, whose hash is not known yet, to dst and returns its
// sha256 and size. HTML pages (login or confirmation pages) are rejected. On
// any error dst is removed.
func (c *Client) Fetch(ctx context.Context, ref, dst string) (sha string, size int64, err error) {
	u, err := c.Resolve(ref)
	if err != nil {
		return "", 0, err
	}
	return c.download(ctx, u, dst, 0, true, nil)
}

// download streams u to dst and returns its sha256 and size. A size > 0 caps
// the read at one byte more, so oversize bodies are detected without reading
// them whole. On any error dst is removed.
func (c *Client) download(ctx context.Context, u, dst string, size int64, rejectHTML bool, progress ProgressFunc) (sha string, n int64, err error) {
	c.log.Info("downloading", "url", u, "size", size)
	resp, err := c.get(ctx, u)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if rejectHTML && strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		return "", 0, fmt.Errorf("%s returned an HTML page, not a file (a login or confirmation page?)", u)
	}

	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", 0, err
	}
	defer func() {
		if cerr := f.Close(); err == nil && cerr != nil {
			err = cerr
		}
		if err != nil {
			os.Remove(dst)
		}
	}()

	total := size
	if total <= 0 && resp.ContentLength > 0 {
		total = resp.ContentLength
	}
	var body io.Reader = resp.Body
	if size > 0 {
		body = io.LimitReader(resp.Body, size+1)
	}
	h := sha256.New()
	pw := &progressWriter{total: total, fn: progress}
	n, err = io.Copy(io.MultiWriter(f, h, pw), body)
	if err != nil {
		return "", 0, fmt.Errorf("could not download %s: %w", u, err)
	}
	pw.flush()
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// progressWriter reports progress at most every 100 ms.
type progressWriter struct {
	done, total int64
	fn          ProgressFunc
	last        time.Time
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if p.fn != nil && time.Since(p.last) >= 100*time.Millisecond {
		p.last = time.Now()
		p.fn(p.done, p.total)
	}
	return len(b), nil
}

func (p *progressWriter) flush() {
	if p.fn != nil {
		p.fn(p.done, p.total)
	}
}
