package main

import (
	"fmt"
	"os"
	"strings"

	"wget/internal/app"

	"github.com/spf13/pflag"
)

func main() {
	var cfg app.Config

	pflag.StringVarP(&cfg.Output, "output", "O", "", "Rename file")
	pflag.StringVarP(&cfg.Path, "path", "P", ".", "Directory")
	pflag.StringVar(&cfg.Limit, "rate-limit", "", "Limit (200k, 1M)")
	pflag.StringVarP(&cfg.Input, "input", "i", "", "Input file")
	pflag.BoolVarP(&cfg.Background, "background", "B", false, "Background mode")
	pflag.BoolVar(&cfg.Mirror, "mirror", false, "Mirror website")
	pflag.BoolVar(&cfg.Convert, "convert-links", false, "Convert links for offline")
	pflag.StringVarP(&cfg.Rej, "reject", "R", "", "Reject extensions")
	pflag.StringVarP(&cfg.Exc, "exclude", "X", "", "Exclude paths")
	pflag.Parse()

	cfg.URLs = pflag.Args()
	for _, url := range strings.Split(cfg.Exc, ",") {
		cfg.Exclude = append(cfg.Exclude, strings.TrimSpace(url))
	}
	for _, url := range strings.Split(cfg.Rej, ",") {
		cfg.Reject = append(cfg.Reject, strings.TrimSpace(url))
	}
	fmt.Println(cfg.Reject)
	fmt.Println(cfg.Exclude)

	if err := app.Run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
