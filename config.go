package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/ghodss/yaml"

	"github.com/criteo/consul-timeline/consul"
	"github.com/criteo/consul-timeline/server"
	"github.com/criteo/consul-timeline/storage/memory"
	"github.com/criteo/consul-timeline/storage/mysql"
	tl "github.com/criteo/consul-timeline/timeline"
)

type Config struct {
	LogLevel  string        `json:"log_level"`
	LogFormat string        `json:"log_format"`
	Storage   string        `json:"storage"`
	Consul    consul.Config `json:"consul"`
	Server    server.Config `json:"server"`
	Mysql     mysql.Config  `json:"mysql"`
	Memory    memory.Config `json:"memory"`
	// Derive maps service meta to the team, app and version columns.
	Derive tl.Derive `json:"derive"`
}

var DefaultConfig = Config{
	LogLevel:  "info",
	LogFormat: "text",
	Storage:   memory.Name,
}

var (
	logLevelFlag    = flag.String("log-level", DefaultConfig.LogLevel, "(debug, info, warn, error)")
	logFormatFlag   = flag.String("log-format", DefaultConfig.LogFormat, "(text, json)")
	configFileFlag  = flag.String("config", "", "Config file path (yaml, json)")
	storageFlag     = flag.String("storage", DefaultConfig.Storage, "Storage backend (mysql, memory)")
	printConfigFlag = flag.Bool("print-config", false, "Print the configuration")

	deriveTeam    = flag.String("derive-team", "", "Service meta keys for the team column, comma separated, first present wins")
	deriveApp     = flag.String("derive-app", "", "Service meta keys for the app column")
	deriveVersion = flag.String("derive-version", "", "Service meta keys for the version column")
)

func FromFlags() Config {
	cfg := Config{
		LogLevel:  *logLevelFlag,
		LogFormat: *logFormatFlag,
		Storage:   *storageFlag,
		Consul:    consul.ConfigFromFlags(),
		Server:    server.ConfigFromFlags(),
		Mysql:     mysql.ConfigFromFlags(),
		Memory:    memory.ConfigFromFlags(),
		Derive:    tl.DefaultDerive,
	}
	if *deriveTeam != "" {
		cfg.Derive.Team = tl.SplitList(*deriveTeam)
	}
	if *deriveApp != "" {
		cfg.Derive.App = tl.SplitList(*deriveApp)
	}
	if *deriveVersion != "" {
		cfg.Derive.Version = tl.SplitList(*deriveVersion)
	}
	return cfg
}

func GetConfig() Config {
	flag.Parse()

	cfg := DefaultConfig
	cfg.Consul = consul.DefaultConfig
	cfg.Server = server.DefaultConfig
	cfg.Mysql = mysql.DefaultConfig
	cfg.Memory = memory.DefaultConfig
	cfg.Derive = tl.DefaultDerive

	if *configFileFlag != "" {
		f, err := os.ReadFile(*configFileFlag)
		if err != nil {
			log.Fatal(err)
		}
		if err := yaml.Unmarshal(f, &cfg); err != nil {
			log.Fatal(err)
		}
	} else {
		cfg = FromFlags()
	}

	if *printConfigFlag {
		b, _ := yaml.Marshal(cfg)
		fmt.Println(string(b))
		os.Exit(0)
	}

	return cfg
}
