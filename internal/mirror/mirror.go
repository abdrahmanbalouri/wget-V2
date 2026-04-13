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

type Options struct {
	OutputPath string
	Convert    bool
	Reject     []string
	Exclude    []string
}

func Run(base string, opts Options) error {
	u, err := url.Parse(base)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}

	domain := u.Host
	if domain == "" {
		return fmt.Errorf("url has no domain")
	}

	c := colly.NewCollector(
		colly.AllowedDomains(domain),
		colly.Async(true),
	)
	c.SetRequestTimeout(30 * time.Second)
	c.Limit(&colly.LimitRule{Parallelism: 4, Delay: 500 * time.Millisecond})

	var scrapingErrors []error
	c.OnError(func(_ *colly.Response, err error) {
		scrapingErrors = append(scrapingErrors, err)
		fmt.Fprintf(os.Stderr, "Scraping error: %v\n", err)
	})

	c.OnHTML("a[href], link[href], img[src], script[src]", func(e *colly.HTMLElement) {
		link := e.Attr("href")
		if link == "" {
			link = e.Attr("src")
		}

		abs := e.Request.AbsoluteURL(link)
		if abs == "" || !strings.HasPrefix(abs, base) {
			return
		}

		lowerAbs := strings.ToLower(abs)
		for _, r := range opts.Reject {
			if strings.HasSuffix(lowerAbs, "."+strings.ToLower(r)) {
				return
			}
		}
		for _, x := range opts.Exclude {
			if strings.Contains(abs, x) {
				return
			}
		}

		if err := e.Request.Visit(abs); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to visit %s: %v\n", abs, err)
			scrapingErrors = append(scrapingErrors, err)
		}
	})

	c.OnResponse(func(r *colly.Response) {
		reqPath := r.Request.URL.Path
		if reqPath == "" || strings.HasSuffix(reqPath, "/") {
			reqPath = path.Join(reqPath, "index.html")
		}

		fPath := filepath.Join(opts.OutputPath, domain, reqPath)
		if err := os.MkdirAll(filepath.Dir(fPath), 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create directory for %s: %v\n", fPath, err)
			scrapingErrors = append(scrapingErrors, err)
			return
		}

		data := r.Body
		if opts.Convert && (strings.HasSuffix(fPath, ".html") || strings.HasSuffix(fPath, ".css")) {
			data = []byte(strings.ReplaceAll(string(data), base, "./"))
		}

		if err := os.WriteFile(fPath, data, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to write %s: %v\n", fPath, err)
			scrapingErrors = append(scrapingErrors, err)
			return
		}

		fmt.Printf("Mirrored: %s\n", fPath)
	})

	fmt.Printf("🚀 Starting mirror of %s\n", base)
	if err := c.Visit(base); err != nil {
		return fmt.Errorf("failed to start mirror: %w", err)
	}

	c.Wait()
	if len(scrapingErrors) > 0 {
		fmt.Fprintf(os.Stderr, "⚠️  Mirror completed with %d errors\n", len(scrapingErrors))
		return fmt.Errorf("mirror had %d errors during scraping", len(scrapingErrors))
	}

	fmt.Println("✅ Mirror completed successfully!")
	return nil
}
