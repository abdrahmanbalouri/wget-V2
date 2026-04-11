package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/gocolly/colly/v2"
	"github.com/jlaffaye/ftp"
	"github.com/schollz/progressbar/v3"
	"github.com/spf13/pflag"
	"golang.org/x/time/rate"
)

var (
	outO, outP, limitS, fileI string
	bg, mirror, convert       bool
	reject, exclude           []string
)

func main() {
	pflag.StringVarP(&outO, "output", "O", "", "Rename file")
	pflag.StringVarP(&outP, "path", "P", ".", "Directory")
	pflag.StringVar(&limitS, "rate-limit", "", "Limit (200k, 1M)")
	pflag.StringVarP(&fileI, "input", "i", "", "Input file")
	pflag.BoolVarP(&bg, "background", "B", false, "Background mode")
	pflag.BoolVar(&mirror, "mirror", false, "Mirror website")
	pflag.BoolVar(&convert, "convert-links", false, "Convert links for offline")
	pflag.StringSliceVarP(&reject, "reject", "R", []string{}, "Reject extensions")
	pflag.StringSliceVarP(&exclude, "exclude", "X", []string{}, "Exclude paths")
	pflag.Parse()

	if bg {
		fmt.Println("Output written to wget-log")
		f, _ := os.Create("wget-log")
		os.Stdout, os.Stderr = f, f
	}

	urls := pflag.Args()
	if fileI != "" {
		f, _ := os.Open(fileI)
		s := bufio.NewScanner(f)
		for s.Scan() {
			if u := strings.TrimSpace(s.Text()); u != "" {
				urls = append(urls, u)
			}
		}
	}

	for _, u := range urls {
		if mirror {
			runMirror(u)
		} else if strings.HasPrefix(u, "ftp://") {
			downloadFTP(u)
		} else {
			downloadHTTP(u)
		}
	}
}

// --- HTTP Downloader ---
func downloadHTTP(targetURL string) {
	fmt.Printf("start at %s\n", time.Now().Format("2006-01-02 15:04:05"))
	resp, err := http.Get(targetURL)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	defer resp.Body.Close()

	fmt.Printf("sending request, awaiting response... status %d OK\n", resp.StatusCode)
	sizeMB := float64(resp.ContentLength) / (1024 * 1024)
	fmt.Printf("content size: %d [~%.2fMB]\n", resp.ContentLength, sizeMB)

	saveStream(resp.Body, targetURL, resp.ContentLength)
}

// --- FTP Downloader ---
func downloadFTP(rawURL string) {
	fmt.Printf("Downloading FTP: %s\n", rawURL)
	u, _ := url.Parse(rawURL)
	host := u.Host
	if !strings.Contains(host, ":") {
		host += ":21"
	}

	c, err := ftp.Dial(host, ftp.DialWithTimeout(5*time.Second))
	if err != nil {
		fmt.Println("FTP Dial Error:", err)
		return
	}
	defer c.Quit()

	_ = c.Login("anonymous", "anonymous")
	r, err := c.Retr(u.Path)
	if err != nil {
		fmt.Println("FTP Retr Error:", err)
		return
	}
	defer r.Close()

	saveStream(r, rawURL, -1)
}

// --- Shared Save Logic ---
func saveStream(r io.Reader, originalURL string, size int64) {
	name := outO
	if name == "" {
		name = path.Base(originalURL)
		if name == "" || name == "." {
			name = "index.html"
		}
	}
	fPath := filepath.Join(outP, name)
	os.MkdirAll(outP, 0755)

	fmt.Printf("saving file to: %s\n", fPath)
	f, _ := os.Create(fPath)
	defer f.Close()

	bar := progressbar.DefaultBytes(size, "Downloading")
	
	var src io.Reader = r
	if limitS != "" {
		l := parseRate(limitS)
		lim := rate.NewLimiter(rate.Limit(l), int(l))
		src = &throt{r: r, l: lim}
	}

	_, _ = io.Copy(io.MultiWriter(f, bar), src)
	fmt.Printf("\nDownloaded [%s]\n", originalURL)
}

// --- Mirror Logic (The Core) ---
func runMirror(base string) {
	u, _ := url.Parse(base)
	domain := u.Host

	c := colly.NewCollector(
		colly.AllowedDomains(domain),
		colly.Async(true),
	)

	// Limit concurrency for stability
	c.Limit(&colly.LimitRule{Parallelism: 4, Delay: 500 * time.Millisecond})

	c.OnHTML("a[href], link[href], img[src], script[src]", func(e *colly.HTMLElement) {
		link := e.Attr("href")
		if link == "" {
			link = e.Attr("src")
		}

		abs := e.Request.AbsoluteURL(link)
		if abs == "" || !strings.HasPrefix(abs, base) {
			return
		}

		// Reject/Exclude logic
		lowerAbs := strings.ToLower(abs)
		for _, r := range reject {
			if strings.HasSuffix(lowerAbs, "."+strings.ToLower(r)) {
				return
			}
		}
		for _, x := range exclude {
			if strings.Contains(abs, x) {
				return
			}
		}

		_ = e.Request.Visit(abs)
	})

	c.OnResponse(func(r *colly.Response) {
		// 1. Path Logic (Handling index.html and folders)
		reqPath := r.Request.URL.Path
		if reqPath == "" || strings.HasSuffix(reqPath, "/") {
			reqPath = path.Join(reqPath, "index.html")
		}

		// 2. Resolve final file path
		fPath := filepath.Join(outP, domain, reqPath)

		// 3. Fix "Not a directory" error: Ensure parent dir exists
		_ = os.MkdirAll(filepath.Dir(fPath), 0755)

		data := r.Body
		// 4. Link Conversion (Simple Replace)
		if convert && (strings.HasSuffix(fPath, ".html") || strings.HasSuffix(fPath, ".css")) {
			data = []byte(strings.ReplaceAll(string(data), base, "./"))
		}

		_ = os.WriteFile(fPath, data, 0644)
		fmt.Printf("Mirrored: %s\n", fPath)
	})

	fmt.Printf("🚀 Starting mirror of %s\n", base)
	_ = c.Visit(base)
	c.Wait()
	fmt.Println("✅ Mirror completed!")
}

// --- Utilities ---
func parseRate(s string) int64 {
	var n int64
	mult := int64(1)
	s = strings.ToLower(s)
	if strings.HasSuffix(s, "k") {
		mult = 1024
		s = s[:len(s)-1]
	} else if strings.HasSuffix(s, "m") {
		mult = 1024 * 1024
		s = s[:len(s)-1]
	}
	_, _ = fmt.Sscanf(s, "%d", &n)
	return n * mult
}

type throt struct {
	r io.Reader
	l *rate.Limiter
}

func (t *throt) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
		_ = t.l.WaitN(context.Background(), n)
	}
	return n, err
}