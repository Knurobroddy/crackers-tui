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
)

const (
	// maxJSONSize caps JSON documents; they are small catalogs.
	maxJSONSize = 32 << 20
	// maxAttempts caps GET attempts after transient failures.
	maxAttempts = 4
	// networkTimeout bounds dialing, the TLS handshake and response headers.
	networkTimeout = 30 * time.Second
	// retryBaseWait is the first retry backoff; it doubles on each attempt.
	retryBaseWait = time.Second
	// maxRetryWait caps both the backoff and a server's Retry-After value.
	maxRetryWait = 30 * time.Second
	// progressInterval limits how often ProgressFunc is called during a download.
	progressInterval = 100 * time.Millisecond
	// downloadFileMode is the permission for newly written download files.
	downloadFileMode = 0o644
)

// ProgressFunc receives bytes done and the expected total (0 if unknown).
type ProgressFunc func(done, total int64)

// Client talks to the remote library.
type Client struct {
	base       *url.URL // nil for a downloader: absolute URLs only
	http       *http.Client
	appVersion string
	retryDelay time.Duration // first retry backoff; retryBaseWait when zero (tests shorten it)
}

// NewClient returns a client for the given base URL. The base is treated as a
// directory: a trailing slash is added if missing.
func NewClient(base, appVersion string) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return nil, fmt.Errorf("invalid remote URL %q: %w", base, err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("invalid remote URL %q: must be http(s)", base)
	}
	if !strings.HasSuffix(parsed.Path, "/") {
		parsed.Path += "/"
	}
	client := NewDownloader(appVersion)
	client.base = parsed
	return client, nil
}

// NewDownloader returns a client without a base URL, for downloading
// absolute URLs only (e.g. by pack authoring tools).
func NewDownloader(appVersion string) *Client {
	dialer := &net.Dialer{Timeout: networkTimeout}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   networkTimeout,
		ResponseHeaderTimeout: networkTimeout,
		ForceAttemptHTTP2:     true,
	}
	return &Client{
		http:       &http.Client{Transport: transport}, // no overall timeout: large downloads
		appVersion: appVersion,
	}
}

// Resolve resolves ref (absolute or relative to the base) to an http(s) URL.
func (c *Client) Resolve(ref string) (string, error) {
	parsed, err := url.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("invalid URL %q: %w", ref, err)
	}
	resolved := parsed
	if c.base != nil {
		resolved = c.base.ResolveReference(parsed)
	}
	if (resolved.Scheme != "http" && resolved.Scheme != "https") || resolved.Host == "" {
		return "", fmt.Errorf("unsupported URL %q: must be an absolute http(s) URL", ref)
	}
	return resolved.String(), nil
}

// FetchGames downloads and checks games.json.
func (c *Client) FetchGames(ctx context.Context) (*Games, error) {
	body, url, err := c.fetchRaw(ctx, "games.json")
	if err != nil {
		return nil, err
	}
	var games Games
	if err := json.Unmarshal(body, &games); err != nil {
		return nil, fmt.Errorf("invalid games.json at %s: %w", url, err)
	}
	if err := CheckSchema("games.json", games.SchemaVersion, config.GamesSchemaVersion); err != nil {
		return nil, err
	}
	if err := CheckMinAppVersion("games.json", games.MinAppVersion, c.appVersion); err != nil {
		return nil, err
	}
	return &games, nil
}

// FetchIndex downloads and checks index.json.
func (c *Client) FetchIndex(ctx context.Context) (*Index, error) {
	body, url, err := c.fetchRaw(ctx, "index.json")
	if err != nil {
		return nil, err
	}
	var index Index
	if err := json.Unmarshal(body, &index); err != nil {
		return nil, fmt.Errorf("invalid index.json at %s: %w", url, err)
	}
	if err := CheckSchema("index.json", index.SchemaVersion, config.IndexSchemaVersion); err != nil {
		return nil, err
	}
	if err := CheckMinAppVersion("index.json", index.MinAppVersion, c.appVersion); err != nil {
		return nil, err
	}
	return &index, nil
}

// FetchManifest downloads a pack manifest and returns it with the hex SHA-256
// of its raw bytes. The manifest is decoded and validated (Manifest.Validate).
func (c *Client) FetchManifest(ctx context.Context, ref string) (*Manifest, string, error) {
	body, url, err := c.fetchRaw(ctx, ref)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(body)
	var manifest Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, "", fmt.Errorf("invalid pack manifest at %s: %w", url, err)
	}
	if err := manifest.Validate(); err != nil {
		return nil, "", err
	}
	return &manifest, hex.EncodeToString(sum[:]), nil
}

// ManifestHash downloads a manifest and returns only the hash of its raw bytes.
func (c *Client) ManifestHash(ctx context.Context, ref string) (string, error) {
	body, _, err := c.fetchRaw(ctx, ref)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// Download streams entry to dst while hashing, then verifies its sha256 and
// size (if > 0). On any error dst is removed.
func (c *Client) Download(ctx context.Context, entry FileEntry, dst string, progress ProgressFunc) error {
	url, err := c.Resolve(entry.URL)
	if err != nil {
		return err
	}
	result, err := c.download(ctx, url, dst, downloadLimits{size: entry.Size}, progress)
	if err != nil {
		return err
	}
	if err := verifyDownload(url, entry, result); err != nil {
		_ = os.Remove(dst)
		return err
	}
	slog.Debug("download verified", "url", url, "bytes", result.size)
	return nil
}

// Fetch downloads ref, whose hash is not known yet, to dst and returns its
// sha256 and size. HTML pages (login or confirmation pages) are rejected. On
// any error dst is removed.
func (c *Client) Fetch(ctx context.Context, ref, dst string) (string, int64, error) {
	url, err := c.Resolve(ref)
	if err != nil {
		return "", 0, err
	}
	result, err := c.download(ctx, url, dst, downloadLimits{rejectHTML: true}, nil)
	if err != nil {
		return "", 0, err
	}
	return result.sha256, result.size, nil
}

// fetchRaw downloads a JSON document and returns its bytes.
func (c *Client) fetchRaw(ctx context.Context, ref string) ([]byte, string, error) {
	url, err := c.Resolve(ref)
	if err != nil {
		return nil, "", err
	}
	slog.Debug("fetch document", "url", url)
	resp, err := c.get(ctx, url)
	if err != nil {
		return nil, url, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxJSONSize+1))
	if err != nil {
		return nil, url, fmt.Errorf("download %s: %w", url, err)
	}
	if len(body) > maxJSONSize {
		return nil, url, fmt.Errorf("%s is too large", url)
	}
	return body, url, nil
}

// get performs a GET and returns the response if the status is 200.
// Transient failures (network errors, HTTP 429 and 5xx) are retried with
// backoff; hosts such as Thunderstore occasionally answer a burst of
// downloads with a 500.
func (c *Client) get(ctx context.Context, rawURL string) (*http.Response, error) {
	delay := c.retryDelay
	if delay == 0 {
		delay = retryBaseWait
	}
	for attempt := 1; ; attempt++ {
		resp, wait, err := c.getOnce(ctx, rawURL)
		if err == nil || wait < 0 || attempt == maxAttempts {
			return resp, err
		}
		if wait == 0 {
			wait = delay
		}
		slog.Warn("download failed, retrying", "url", rawURL, "attempt", attempt, "in", wait, "err", err)
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(wait):
		}
		delay *= 2
	}
}

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
			return nil, -1, fmt.Errorf("download %s: %w", rawURL, err)
		}
		return nil, 0, fmt.Errorf("download %s: %w", rawURL, err)
	}
	if resp.StatusCode == http.StatusOK {
		return resp, 0, nil
	}
	_ = resp.Body.Close()
	err = fmt.Errorf("download %s: HTTP %s", rawURL, resp.Status)
	if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
		return nil, -1, err
	}
	if s, parseErr := strconv.Atoi(resp.Header.Get("Retry-After")); parseErr == nil && s > 0 {
		wait = min(time.Duration(s)*time.Second, maxRetryWait)
	}
	return nil, wait, err
}

// download streams url to dst and returns its sha256 and size. On any error
// dst is removed.
func (c *Client) download(ctx context.Context, url, dst string, limits downloadLimits, progress ProgressFunc) (downloadResult, error) {
	slog.Debug("download", "url", url, "size", limits.size)
	resp, err := c.get(ctx, url)
	if err != nil {
		return downloadResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if limits.rejectHTML && isHTML(resp) {
		return downloadResult{}, fmt.Errorf("fetch %s: got an HTML page, not a file", url)
	}
	result, err := writeHashed(dst, limitedBody(resp, limits.size), totalSize(resp, limits.size), progress)
	if err != nil {
		_ = os.Remove(dst)
		return downloadResult{}, fmt.Errorf("download %s: %w", url, err)
	}
	return result, nil
}

// downloadLimits bounds one download.
type downloadLimits struct {
	size       int64 // > 0: expected size; the read is capped at size+1
	rejectHTML bool
}

// downloadResult is the outcome of a completed download.
type downloadResult struct {
	sha256 string
	size   int64
}

// verifyDownload checks a completed download against the expected entry.
func verifyDownload(url string, entry FileEntry, result downloadResult) error {
	if entry.Size > 0 && result.size != entry.Size {
		return fmt.Errorf("size mismatch for %s: want %d bytes, got %d", url, entry.Size, result.size)
	}
	if !strings.EqualFold(result.sha256, entry.SHA256) {
		return fmt.Errorf("checksum mismatch for %s: want sha256 %s, got %s", url, strings.ToLower(entry.SHA256), result.sha256)
	}
	return nil
}

// writeHashed copies body to dst while hashing and reporting progress, and
// returns its sha256 and size.
func writeHashed(dst string, body io.Reader, total int64, progress ProgressFunc) (downloadResult, error) {
	file, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, downloadFileMode)
	if err != nil {
		return downloadResult{}, err
	}
	hash := sha256.New()
	tracker := &progressWriter{total: total, fn: progress}
	n, copyErr := io.Copy(io.MultiWriter(file, hash, tracker), body)
	closeErr := file.Close()
	if copyErr != nil {
		return downloadResult{}, copyErr
	}
	tracker.flush()
	if closeErr != nil {
		return downloadResult{}, closeErr
	}
	return downloadResult{sha256: hex.EncodeToString(hash.Sum(nil)), size: n}, nil
}

// isHTML reports whether resp looks like an HTML page rather than a file.
func isHTML(resp *http.Response) bool {
	return strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html")
}

// limitedBody caps resp.Body at size+1 when size is known, so oversize
// bodies are detected without reading them whole.
func limitedBody(resp *http.Response, size int64) io.Reader {
	if size <= 0 {
		return resp.Body
	}
	return io.LimitReader(resp.Body, size+1)
}

// totalSize picks the expected total: the known size, else the response's
// content length (0 if neither is known).
func totalSize(resp *http.Response, size int64) int64 {
	if size > 0 {
		return size
	}
	if resp.ContentLength > 0 {
		return resp.ContentLength
	}
	return size
}

// progressWriter reports progress at most every progressInterval.
type progressWriter struct {
	done, total int64
	fn          ProgressFunc
	last        time.Time
}

func (p *progressWriter) Write(data []byte) (int, error) {
	p.done += int64(len(data))
	if p.fn != nil && time.Since(p.last) >= progressInterval {
		p.last = time.Now()
		p.fn(p.done, p.total)
	}
	return len(data), nil
}

func (p *progressWriter) flush() {
	if p.fn != nil {
		p.fn(p.done, p.total)
	}
}
