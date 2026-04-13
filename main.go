package main

import (
	"fmt"
	"os"

	"github.com/spf13/pflag"
	"wget/internal/app"
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
	pflag.StringSliceVarP(&cfg.Reject, "reject", "R", []string{}, "Reject extensions")
	pflag.StringSliceVarP(&cfg.Exclude, "exclude", "X", []string{}, "Exclude paths")
	pflag.Parse()

	cfg.URLs = pflag.Args()

	if err := app.Run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
