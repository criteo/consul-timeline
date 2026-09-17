package server

import "flag"

type Config struct {
	ListenAddr string `json:"listen"`
	// StaticDir serves the UI from a directory instead of the embedded
	// copy, for development.
	StaticDir string `json:"static_dir"`
}

var DefaultConfig = Config{
	ListenAddr: ":8888",
}

var flagConfig Config

func init() {
	flag.StringVar(&flagConfig.ListenAddr, "listen", DefaultConfig.ListenAddr, "Server listen address")
	flag.StringVar(&flagConfig.StaticDir, "static-dir", DefaultConfig.StaticDir, "Serve the UI from this directory instead of the embedded files")
}

func ConfigFromFlags() Config {
	return flagConfig
}
