package app

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"wget/internal/download"
	"wget/internal/mirror"
)

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
	if cfg.Background {
		fmt.Println("Output written to wget-log")
		f, err := os.Create("wget-log")
		if err != nil {
			return fmt.Errorf("cannot create wget-log: %w", err)
		}
		defer f.Close()
		os.Stdout, os.Stderr = f, f
	}

	urls := cfg.URLs
	if cfg.Input != "" {
		f, err := os.Open(cfg.Input)
		if err != nil {
			return fmt.Errorf("cannot open input file '%s': %w", cfg.Input, err)
		}
		defer f.Close()

		s := bufio.NewScanner(f)
		for s.Scan() {
			if u := strings.TrimSpace(s.Text()); u != "" {
				urls = append(urls, u)
			}
		}

		if err := s.Err(); err != nil {
			return fmt.Errorf("error reading input file: %w", err)
		}
	}

	if len(urls) == 0 {
		return fmt.Errorf("no URLs provided\nUsage: ./wget [flags] <URL> [URL ...]")
	}

	hasErrors := false

	for _, u := range urls {
		var err error
		if cfg.Mirror {
			err = mirror.Run(u, mirror.Options{
				OutputPath: cfg.Path,
				Convert:    cfg.Convert,
				Reject:     cfg.Reject,
				Exclude:    cfg.Exclude,
			})
		} else if strings.HasPrefix(u, "ftp://") {
			err = download.FTP(download.Options{
				Output: cfg.Output,
				Path:   cfg.Path,
				Limit:  cfg.Limit,
			}, u)
		} else {
			err = download.HTTP(download.Options{
				Output: cfg.Output,
				Path:   cfg.Path,
				Limit:  cfg.Limit,
			}, u)
		}

		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to process '%s': %v\n", u, err)
			hasErrors = true
			continue
		}
	}

	if hasErrors {
		return fmt.Errorf("some downloads failed")
	}

	return nil
}
