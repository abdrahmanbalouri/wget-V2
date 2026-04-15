package mirror

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gocolly/colly/v2"
)

// Options for mirroring a website.
type Options struct {
	Convert bool     // --convert-links
	Reject  []string // -R reject extensions
	Exclude []string // -X exclude paths
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
	c.Limit(&colly.LimitRule{Parallelism: 4, Delay: 300 * time.Millisecond})

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
		localPath := urlToLocalPath(domain, r.Request.URL)
		os.MkdirAll(filepath.Dir(localPath), 0755)
		if err := os.WriteFile(localPath, r.Body, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "write error: %s - %v\n", localPath, err)
			return
		}

		mu.Lock()
		saved[r.Request.URL.String()] = localPath
		mu.Unlock()

		fmt.Printf("saved: %s\n", localPath)

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
	if err := c.Visit(base); err != nil {
		return err
	}
	c.Wait()

	if opts.Convert {
		convertLinks(domain, hostname, saved)
	}

	fmt.Println("Mirror done.")
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
		if strings.EqualFold(ext, r) {
			return true
		}
	}
	return false
}

func isExcluded(urlPath string, exclude []string) bool {
	for _, x := range exclude {
		if strings.HasPrefix(urlPath, x) {
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
			os.WriteFile(localPath, []byte(converted), 0644)
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
