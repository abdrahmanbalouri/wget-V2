package app

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"

	"wget/internal/download"
	"wget/internal/mirror"
)

// Config holds all the CLI flags.
type Config struct {
	Output     string
	Path       string
	Limit      string
	Input      string
	Background bool
	Mirror     bool
	Convert    bool
	Reject     []string
	Exclude    []string
	URLs       []string
}

func Run(cfg Config) error {
	// -B flag: spawn a background child process, parent exits immediately.
	if cfg.Background && os.Getenv("WGET_BG") == "" {
		fmt.Println("Output will be written to \"wget-log\".")
		log, err := os.Create("wget-log")
		if err != nil {
			return err
		}
		
		defer log.Close()

		os.Setenv("WGET_BG", "1")
		p, err := os.StartProcess(os.Args[0], os.Args, &os.ProcAttr{
			Files: []*os.File{os.Stdin, log, log},
			Env:   os.Environ(),
		})
		if err != nil {
			return err
		}
		p.Release()
		return nil
	}

	// Child process keeps Background=true so progress bar is skipped.
	if os.Getenv("WGET_BG") == "1" {
		cfg.Background = true
	}

	// Collect URLs from -i file.
	urls := cfg.URLs
	if cfg.Input != "" {
		f, err := os.Open(cfg.Input)
		if err != nil {
			return err
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if line := strings.TrimSpace(sc.Text()); line != "" {
				urls = append(urls, line)
			}
		}
	}

	if len(urls) == 0 {
		return fmt.Errorf("no URLs provided")
	}

	return asyncDownload(cfg, urls)
}

// asyncDownload downloads all URLs concurrently using goroutines.
func asyncDownload(cfg Config, urls []string) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error

	for _, u := range urls {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			if err := process(cfg, url); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(u)
	}
	wg.Wait()

	fmt.Printf("\nDownload finished: %v\n", urls)

	if len(errs) > 0 {
		return fmt.Errorf("some downloads failed")
	}
	return nil
}

// process handles a single URL: mirror or download.
func process(cfg Config, u string) error {
	if cfg.Mirror {
		return mirror.Run(u, mirror.Options{
			Convert: cfg.Convert,
			Reject:  cfg.Reject,
			Exclude: cfg.Exclude,
		})
	}
	return download.File(u, download.Options{
		Output:     cfg.Output,
		Path:       cfg.Path,
		Limit:      cfg.Limit,
		Background: cfg.Background,
	})
}
