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

	"github.com/schollz/progressbar/v3"
	"golang.org/x/time/rate"
)

// Options
type Options struct {
	Output     string
	Path       string
	Limit      string
	Background bool
}

// -------------------- ENTRY --------------------

func File(rawURL string, opt Options) error {
	fmt.Printf("start at %s\n", time.Now().Format("2006-01-02 15:04:05"))

	// background mode
	if opt.Background {
		go func() {
			_ = download(rawURL, opt)
		}()
		fmt.Println("running in background...")
		return nil
	}

	return download(rawURL, opt)
}

// -------------------- DOWNLOAD CORE --------------------

func download(rawURL string, opt Options) error {
	fmt.Print("sending request... ")

	resp, err := http.Get(rawURL)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	fmt.Printf("status: %s\n", resp.Status)

	if resp.StatusCode != 200 {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	fmt.Printf("content size: %d (~%.2fMB)\n",
		resp.ContentLength,
		float64(resp.ContentLength)/1024/1024,
	)

	name := fileName(opt.Output, rawURL)
	saveTo := buildPath(opt.Path, name)

	fmt.Printf("saving to: %s\n", saveTo)

	os.MkdirAll(filepath.Dir(saveTo), 0o755)

	file, err := os.Create(saveTo)
	if err != nil {
		return fmt.Errorf("cannot create file: %w", err)
	}
	defer file.Close()

	// -------------------- RATE LIMIT --------------------

	var reader io.Reader = resp.Body

	if opt.Limit != "" {
		r := parseRate(opt.Limit)
		if r > 0 {
			fmt.Printf("rate limit: %s => %d B/s\n", opt.Limit, r)
			lim := rate.NewLimiter(rate.Limit(r), 1)

			reader = &throttle{
				r:   resp.Body,
				lim: lim,
			}
		}
	}

	// -------------------- PROGRESS --------------------

	bar := progressbar.DefaultBytes(resp.ContentLength, "downloading")

	_, err = io.Copy(io.MultiWriter(file, bar), reader)

	fmt.Println()

	// IMPORTANT FIX: only real errors
	if err != nil && err != io.EOF {
		return fmt.Errorf("download error: %w", err)
	}

	fmt.Printf("Download finished: %s\n", rawURL)
	return nil
}

// -------------------- THROTTLE --------------------

type throttle struct {
	r   io.Reader
	lim *rate.Limiter
}

func (t *throttle) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)

	if n > 0 {
		_ = t.lim.WaitN(context.Background(), n)
	}

	return n, err
}

// -------------------- HELPERS --------------------

func fileName(output, rawURL string) string {
	if output != "" {
		return output
	}
	u, _ := url.Parse(rawURL)
	name := path.Base(u.Path)
	if name == "" || name == "." {
		return "index.html"
	}
	return name
}

func buildPath(dir, name string) string {
	if strings.HasPrefix(dir, "~/") {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, dir[2:])
	}
	return filepath.Join(dir, name)
}

func parseRate(s string) int64 {
	s = strings.TrimSpace(strings.ToLower(s))
	mult := int64(1)

	if strings.HasSuffix(s, "k") {
		mult = 1024
		s = strings.TrimSuffix(s, "k")
	} else if strings.HasSuffix(s, "m") {
		mult = 1024 * 1024
		s = strings.TrimSuffix(s, "m")
	}

	var n int64
	fmt.Sscanf(s, "%d", &n)
	return n * mult
}
