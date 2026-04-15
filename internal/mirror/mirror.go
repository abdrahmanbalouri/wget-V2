package mirror

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
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

	c := colly.NewCollector(
		colly.AllowedDomains(domain),
		colly.Async(true),
	)
	c.SetRequestTimeout(30 * time.Second)
	c.Limit(&colly.LimitRule{Parallelism: 4, Delay: 500 * time.Millisecond})

	// Follow links in a, link, img, script tags.
	c.OnHTML("a[href], link[href], img[src], script[src]", func(e *colly.HTMLElement) {
		link := e.Attr("href")
		if link == "" {
			link = e.Attr("src")
		}

		abs := e.Request.AbsoluteURL(link)
		if abs == "" {
			return
		}

		parsed, err := url.Parse(abs)
		if err != nil || parsed.Host != domain {
			return
		}

		// Skip rejected extensions (e.g. --reject=gif).
		ext := strings.TrimPrefix(path.Ext(parsed.Path), ".")
		for _, r := range opts.Reject {
			if strings.EqualFold(ext, r) {
				return
			}
		}

		// Skip excluded paths (e.g. -X=/img).
		for _, x := range opts.Exclude {
			if strings.HasPrefix(parsed.Path, x) {
				return
			}
		}

		e.Request.Visit(abs)
	})

	// Save each response to a local file.
	c.OnResponse(func(r *colly.Response) {
		p := r.Request.URL.Path
		if p == "" || strings.HasSuffix(p, "/") {
			p = path.Join(p, "index.html")
		}

		fPath := filepath.Join(domain, p)
		os.MkdirAll(filepath.Dir(fPath), 0755)

		data := r.Body

		// --convert-links: replace absolute URLs with relative paths.
		if opts.Convert && isText(fPath) {
			s := string(data)
			s = strings.ReplaceAll(s, "https://"+domain+"/", "./")
			s = strings.ReplaceAll(s, "https://"+domain, "./")
			s = strings.ReplaceAll(s, "http://"+domain+"/", "./")
			s = strings.ReplaceAll(s, "http://"+domain, "./")
			s = strings.ReplaceAll(s, "//"+domain+"/", "./")
			s = strings.ReplaceAll(s, "//"+domain, "./")
			data = []byte(s)
		}

		os.WriteFile(fPath, data, 0644)
		fmt.Printf("saved: %s\n", fPath)
	})

	fmt.Printf("Mirroring %s ...\n", base)
	if err := c.Visit(base); err != nil {
		return err
	}
	c.Wait()
	fmt.Println("Mirror done.")
	return nil
}

// isText checks if a file is HTML, CSS, or JS (for link conversion).
func isText(name string) bool {
	name = strings.ToLower(name)
	return strings.HasSuffix(name, ".html") ||
		strings.HasSuffix(name, ".css") ||
		strings.HasSuffix(name, ".js")
}
