package mirror

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gocolly/colly/v2"
	"github.com/schollz/progressbar/v3"
	"golang.org/x/time/rate"
)

// Options for mirroring a website.
type Options struct {
	Convert    bool     // --convert-links
	Reject     []string // -R reject extensions
	Exclude    []string // -X exclude paths
	Background bool     // -B background mode
	Limit      string   // --rate-limit
}

// Run mirrors a website by crawling all pages and saving files locally.
func Run(base string, opts Options) error {
	u, err := url.Parse(base)
	if err != nil {
		return err
	}
	domain := u.Host
	hostname := u.Hostname()

	c := colly.NewCollector(
		colly.AllowedDomains(domain, hostname),
		colly.Async(true),
	)
	c.SetRequestTimeout(30 * time.Second)
	c.Limit(&colly.LimitRule{
		Parallelism: 4,
		Delay:       300 * time.Millisecond,
	})

	// Track saved files for --convert-links.
	var mu sync.Mutex
	saved := make(map[string]string) // absolute URL → local file path

	// visit validates and queues a URL for crawling.
	visit := func(e *colly.HTMLElement, rawURL string) {
		abs := e.Request.AbsoluteURL(rawURL)
		if abs == "" {
			return
		}
		p, err := url.Parse(abs)
		if err != nil || !isSameHost(p.Host, domain, hostname) {
			return
		}
		if isRejected(p.Path, opts.Reject) || isExcluded(p.Path, opts.Exclude) {
			return
		}
		e.Request.Visit(abs)
	}

	// Crawl: <a href>, <link href>, <script src>, <img src>
	c.OnHTML("a[href]", func(e *colly.HTMLElement) { visit(e, e.Attr("href")) })
	c.OnHTML("link[href]", func(e *colly.HTMLElement) { visit(e, e.Attr("href")) })
	c.OnHTML("script[src]", func(e *colly.HTMLElement) { visit(e, e.Attr("src")) })
	c.OnHTML("img[src]", func(e *colly.HTMLElement) { visit(e, e.Attr("src")) })

	// <img srcset="url1 1x, url2 2x"> — each entry is "url [descriptor]"
	c.OnHTML("img[srcset]", func(e *colly.HTMLElement) {
		for _, part := range strings.Split(e.Attr("srcset"), ",") {
			fields := strings.Fields(strings.TrimSpace(part))
			if len(fields) > 0 {
				visit(e, fields[0])
			}
		}
	})

	// Save each response to disk.
	c.OnResponse(func(r *colly.Response) {
		if !opts.Background {
			// Print status and content info like download module
			fmt.Printf("status %d\n", r.StatusCode)
		}
		if r.StatusCode != 200 {
			if !opts.Background {
				fmt.Fprintf(os.Stderr, "skipping non-200 response: %s\n", r.Request.URL)
			}
			return
		}
		size := len(r.Body)
		if !opts.Background {
			mb := float64(size) / (1024 * 1024)
			fmt.Printf("content size: %d [~%.2fMB]\n", size, mb)
		}

		localPath := urlToLocalPath(domain, r.Request.URL)
		if !opts.Background {
			fmt.Printf("saving file to: %s\n", localPath)
		}

		os.MkdirAll(filepath.Dir(localPath), 0o755)

		// Create the output file.
		f, err := os.Create(localPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "write error: %s - %v\n", localPath, err)
			return
		}
		defer f.Close()

		// Apply rate limit if set.
		var src io.Reader = bytes.NewReader(r.Body)
		if opts.Limit != "" {
			if r := parseRate(opts.Limit); r > 0 {
				lim := rate.NewLimiter(rate.Limit(r), int(r))
				src = &throttle{r: src, lim: lim}
			}
		}

		if !opts.Background {
			// Write with progress bar
			bar := progressbar.DefaultBytes(int64(size), "Saving "+filepath.Base(localPath))
			_, err = io.Copy(io.MultiWriter(f, bar), src)
			fmt.Println()
		} else {
			// Write silently
			_, err = io.Copy(f, src)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "write error: %s - %v\n", localPath, err)
			return
		}

		mu.Lock()
		saved[r.Request.URL.String()] = localPath
		mu.Unlock()

		if !opts.Background {
			fmt.Printf("saved: %s\n", localPath)
			fmt.Printf("Downloaded [%s]\n", r.Request.URL)
		}

		// If it's CSS, extract and queue url() references.
		if strings.Contains(r.Headers.Get("Content-Type"), "css") {
			for _, u := range extractCSSURLs(string(r.Body)) {
				abs := resolveURL(r.Request.URL, u)
				if abs == "" {
					continue
				}
				p, _ := url.Parse(abs)
				if p != nil && isSameHost(p.Host, domain, hostname) &&
					!isRejected(p.Path, opts.Reject) && !isExcluded(p.Path, opts.Exclude) {
					r.Request.Visit(abs)
				}
			}
		}
	})

	c.OnError(func(r *colly.Response, err error) {
		fmt.Fprintf(os.Stderr, "error: %s - %v\n", r.Request.URL, err)
	})

	fmt.Printf("Mirroring %s ...\n", base)
	fmt.Printf("start at %s\n", time.Now().Format("2006-01-02 15:04:05"))
	if err := c.Visit(base); err != nil {
		return err
	}
	c.Wait()

	if opts.Convert {
		convertLinks(domain, hostname, saved)
	}

	fmt.Println("Mirror done.")
	fmt.Printf("finished at %s\n", time.Now().Format("2006-01-02 15:04:05"))
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func isSameHost(host, domain, hostname string) bool {
	return host == domain || host == hostname
}

func isRejected(urlPath string, reject []string) bool {
	ext := strings.TrimPrefix(path.Ext(urlPath), ".")
	for _, r := range reject {
		if strings.EqualFold(ext, strings.TrimSpace(r)) {
			return true
		}
	}
	return false
}

func isExcluded(urlPath string, exclude []string) bool {
	for _, x := range exclude {
		if strings.HasPrefix(urlPath, strings.TrimSpace(x)) {
			return true
		}
	}
	return false
}

// urlToLocalPath maps a URL to a local file path under the domain directory.
func urlToLocalPath(domain string, u *url.URL) string {
	p := u.Path
	if p == "" || strings.HasSuffix(p, "/") {
		p = path.Join(p, "index.html")
	}
	p = strings.TrimPrefix(p, "/")
	return filepath.Join(domain, filepath.FromSlash(p))
}

// resolveURL resolves a possibly-relative URL string against a base URL.
func resolveURL(base *url.URL, ref string) string {
	r, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	resolved := base.ResolveReference(r)
	// Drop query & fragment for asset fetching.
	resolved.RawQuery = ""
	resolved.Fragment = ""
	return resolved.String()
}

// extractCSSURLs returns all URLs found inside url() in CSS content.
var cssURLRe = regexp.MustCompile(`(?i)url\(\s*['"]?([^'"\)\s]+)['"]?\s*\)`)

func extractCSSURLs(css string) []string {
	var urls []string
	for _, m := range cssURLRe.FindAllStringSubmatch(css, -1) {
		if len(m) > 1 && m[1] != "" {
			urls = append(urls, m[1])
		}
	}
	return urls
}

// ---------------------------------------------------------------------------
// --convert-links post-processing
// ---------------------------------------------------------------------------

func convertLinks(domain, hostname string, saved map[string]string) {
	for _, localPath := range saved {
		if !isTextFile(localPath) {
			continue
		}
		data, err := os.ReadFile(localPath)
		if err != nil {
			continue
		}
		original := string(data)
		var converted string
		if strings.HasSuffix(strings.ToLower(localPath), ".css") {
			converted = convertCSSLinks(original, localPath, domain, hostname)
		} else {
			converted = convertHTMLLinks(original, localPath, domain, hostname)
		}
		if converted != original {
			os.WriteFile(localPath, []byte(converted), 0o644)
			fmt.Printf("converted links: %s\n", localPath)
		}
	}
}

var (
	htmlAttrDouble = regexp.MustCompile(`(?i)((?:href|src|action)\s*=\s*)"([^"]*)"`)
	htmlAttrSingle = regexp.MustCompile(`(?i)((?:href|src|action)\s*=\s*)'([^']*)'`)
	htmlSrcset     = regexp.MustCompile(`(?i)(srcset\s*=\s*)"([^"]*)"`)
)

func convertHTMLLinks(content, currentFile, domain, hostname string) string {
	replace := func(prefix, rawURL string) string {
		return prefix + `"` + toRel(rawURL, currentFile, domain, hostname) + `"`
	}
	content = htmlAttrDouble.ReplaceAllStringFunc(content, func(m string) string {
		sub := htmlAttrDouble.FindStringSubmatch(m)
		if len(sub) < 3 {
			return m
		}
		return replace(sub[1], sub[2])
	})
	content = htmlAttrSingle.ReplaceAllStringFunc(content, func(m string) string {
		sub := htmlAttrSingle.FindStringSubmatch(m)
		if len(sub) < 3 {
			return m
		}
		return sub[1] + `"` + toRel(sub[2], currentFile, domain, hostname) + `"`
	})
	// Convert srcset entries.
	content = htmlSrcset.ReplaceAllStringFunc(content, func(m string) string {
		sub := htmlSrcset.FindStringSubmatch(m)
		if len(sub) < 3 {
			return m
		}
		parts := strings.Split(sub[2], ",")
		for i, p := range parts {
			fields := strings.Fields(strings.TrimSpace(p))
			if len(fields) > 0 {
				fields[0] = toRel(fields[0], currentFile, domain, hostname)
				parts[i] = strings.Join(fields, " ")
			}
		}
		return sub[1] + `"` + strings.Join(parts, ", ") + `"`
	})
	return content
}

func convertCSSLinks(content, currentFile, domain, hostname string) string {
	return cssURLRe.ReplaceAllStringFunc(content, func(m string) string {
		sub := cssURLRe.FindStringSubmatch(m)
		if len(sub) < 2 {
			return m
		}
		return `url("` + toRel(sub[1], currentFile, domain, hostname) + `")`
	})
}

// toRel converts an absolute/root-relative URL to a relative path for offline use.
func toRel(rawURL, currentFile, domain, hostname string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}

	var targetURLPath string
	switch {
	case u.Host != "":
		if !isSameHost(u.Host, domain, hostname) {
			return rawURL // external link — leave as-is
		}
		targetURLPath = u.Path
	case strings.HasPrefix(rawURL, "/"):
		targetURLPath = u.Path
	default:
		return rawURL // already relative, or mailto:, javascript:, data:, #…
	}

	// Normalise directory paths.
	if targetURLPath == "" || strings.HasSuffix(targetURLPath, "/") {
		targetURLPath = path.Join(targetURLPath, "index.html")
	}
	targetURLPath = strings.TrimPrefix(targetURLPath, "/")

	targetFile := filepath.Join(domain, filepath.FromSlash(targetURLPath))

	// Only convert the link if the file was actually downloaded.
	// If it's missing (e.g. the server returned a 404), keep the original URL
	// so the browser can still reach the live site instead of a broken local path.
	if _, err := os.Stat(targetFile); err != nil {
		return rawURL
	}

	rel, err := filepath.Rel(filepath.Dir(currentFile), targetFile)
	if err != nil {
		return rawURL
	}
	result := filepath.ToSlash(rel)
	if u.RawQuery != "" {
		result += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		result += "#" + u.Fragment
	}
	return result
}

func isTextFile(name string) bool {
	for _, ext := range []string{".html", ".htm", ".css", ".js", ".xhtml", ".svg", ".xml"} {
		if strings.HasSuffix(strings.ToLower(name), ext) {
			return true
		}
	}
	return false
}

// throttle wraps a reader to limit read speed.
type throttle struct {
	r   io.Reader
	lim *rate.Limiter
}

func (t *throttle) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
		t.lim.WaitN(context.Background(), n)
	}
	return n, err
}

// parseRate converts "400k" or "2M" to bytes per second.
func parseRate(s string) int64 {
	s = strings.TrimSpace(strings.ToLower(s))
	mult := int64(1)
	if strings.HasSuffix(s, "k") {
		mult = 1024
		s = s[:len(s)-1]
	} else if strings.HasSuffix(s, "m") {
		mult = 1024 * 1024
		s = s[:len(s)-1]
	}
	var n int64
	fmt.Sscanf(s, "%d", &n)
	return n * mult
}
