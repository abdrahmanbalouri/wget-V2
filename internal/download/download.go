package download

import (
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

	"github.com/jlaffaye/ftp"
	"github.com/schollz/progressbar/v3"
	"golang.org/x/time/rate"
)

type Options struct {
	Output string
	Path   string
	Limit  string
}

func HTTP(opt Options, targetURL string) error {
	fmt.Printf("start at %s\n", time.Now().Format("2006-01-02 15:04:05"))

	client := &http.Client{Timeout: 1000 * time.Minute}
	resp, err := client.Get(targetURL)
	if err != nil {
		return fmt.Errorf("error downloading %s: %w", targetURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http error %d for %s", resp.StatusCode, targetURL)
	}

	fmt.Printf("sending request, awaiting response... status %d OK\n", resp.StatusCode)
	sizeMB := float64(resp.ContentLength) / (1024 * 1024)
	fmt.Printf("content size: %d [~%.2fMB]\n", resp.ContentLength, sizeMB)

	if err := saveStream(resp.Body, targetURL, opt, resp.ContentLength); err != nil {
		return err
	}

	fmt.Printf("finished at %s\n", time.Now().Format("2006-01-02 15:04:05"))
	return nil
}

func FTP(opt Options, rawURL string) error {
	fmt.Printf("Downloading FTP: %s\n", rawURL)

	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("error parsing ftp url: %w", err)
	}

	host := u.Host
	if host == "" {
		return fmt.Errorf("invalid ftp url: no host specified")
	}
	if !strings.Contains(host, ":") {
		host += ":21"
	}

	c, err := ftp.Dial(host, ftp.DialWithTimeout(5*time.Second))
	if err != nil {
		return fmt.Errorf("ftp dial failed for %s: %w", host, err)
	}
	defer c.Quit()

	if err := c.Login("anonymous", "anonymous"); err != nil {
		return fmt.Errorf("ftp login failed: %w", err)
	}

	r, err := c.Retr(u.Path)
	if err != nil {
		return fmt.Errorf("ftp retrieve failed for %s: %w", u.Path, err)
	}
	defer r.Close()

	if err := saveStream(r, rawURL, opt, -1); err != nil {
		return err
	}

	fmt.Printf("finished at %s\n", time.Now().Format("2006-01-02 15:04:05"))
	return nil
}

func saveStream(r io.Reader, originalURL string, opt Options, size int64) error {
	name := opt.Output
	if name == "" {
		name = path.Base(originalURL)
		if name == "" || name == "." {
			name = "index.html"
		}
	}

	fPath := filepath.Join(opt.Path, name)
	absPath, err := filepath.Abs(fPath)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	fmt.Printf("saving file to: %s\n", absPath)
	f, err := os.Create(absPath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer f.Close()

	bar := progressbar.DefaultBytes(size, "Downloading")
	var src io.Reader = r
	if opt.Limit != "" {
		l := parseRate(opt.Limit)
		lim := rate.NewLimiter(rate.Limit(l), int(l))
		src = &throt{r: r, l: lim}
	}

	bytesWritten, err := io.Copy(io.MultiWriter(f, bar), src)
	if err != nil {
		return fmt.Errorf("error during download: %w", err)
	}

	if bytesWritten == 0 && size > 0 {
		return fmt.Errorf("no bytes downloaded (expected ~%d bytes)", size)
	}

	fmt.Printf("\nDownloaded [%s]\n", originalURL)
	return nil
}

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
