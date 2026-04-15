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

// Options for downloading a file.
type Options struct {
	Output     string // -O rename
	Path       string // -P directory
	Limit      string // --rate-limit
	Background bool   // -B background mode
}

// File downloads a single URL and saves it to disk.
func File(rawURL string, opt Options) error {
	fmt.Printf("start at %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Print("sending request, awaiting response... ")

	resp, err := http.Get(rawURL)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Print real status and stop if not 200.
	fmt.Printf("status %s\n", resp.Status)
	if resp.StatusCode != 200 {
		return fmt.Errorf("server returned %s", resp.Status)
	}

	// Print content size in bytes and MB.
	mb := float64(resp.ContentLength) / (1024 * 1024)
	fmt.Printf("content size: %d [~%.2fMB]\n", resp.ContentLength, mb)

	// Determine file name and save path.
	name := fileName(opt.Output, rawURL)
	saveTo := buildPath(opt.Path, name)
	displayTo := displayPath(opt.Path, name)

	fmt.Printf("saving file to: %s\n", displayTo)

	// Create directories if needed.
	os.MkdirAll(filepath.Dir(saveTo), 0755)

	// Create the output file.
	f, err := os.Create(saveTo)
	if err != nil {
		return fmt.Errorf("cannot create file: %w", err)
	}
	defer f.Close()

	// Apply rate limit if set.
	var src io.Reader = resp.Body
	if opt.Limit != "" {
		if r := parseRate(opt.Limit); r > 0 {
			lim := rate.NewLimiter(rate.Limit(r), int(r))
			src = &throttle{r: src, lim: lim}
		}
	}

	// Download: with progress bar (normal) or silent (background).
	
		bar := progressbar.DefaultBytes(resp.ContentLength, "")
		_, err = io.Copy(io.MultiWriter(f, bar), src)
		fmt.Println()
	if err != nil {
		return fmt.Errorf("download error: %w", err)
	}

	fmt.Printf("Downloaded [%s]\n", rawURL)
	fmt.Printf("finished at %s\n", time.Now().Format("2006-01-02 15:04:05"))
	return nil
}

// fileName returns the output file name.
func fileName(output, rawURL string) string {
	if output != "" {
		return output
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "index.html"
	}
	name := path.Base(u.Path)
	if name == "" || name == "." || name == "/" {
		return "index.html"
	}
	return name
}

// buildPath creates the real file system path (expands ~).
func buildPath(dir, name string) string {
	if strings.HasPrefix(dir, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, dir[2:])
		}
	}
	return filepath.Join(dir, name)
}

// displayPath creates the path shown to the user.
func displayPath(dir, name string) string {
	if dir == "" || dir == "." {
		return "./" + name
	}
	return filepath.Join(dir, name)
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
