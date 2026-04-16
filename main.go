package main

import (
	"fmt"
	"os"

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
	pflag.StringVarP(&cfg.Reject, "reject", "R", "", "Reject URLs (comma-separated)")
	 pflag.StringVarP(&cfg.Exclude, "exclude" , "X", "", "Exclude URLs (comma-separated)")
	pflag.Parse()

	cfg.URLs = pflag.Args()
	 fmt.Println(cfg.Reject, cfg.Exclude)
	if err := app.Run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
