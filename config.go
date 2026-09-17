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
)

type Config struct {
	LogLevel string        `json:"log_level"`
	Storage  string        `json:"storage"`
	Consul   consul.Config `json:"consul"`
	Server   server.Config `json:"server"`
	Mysql    mysql.Config  `json:"mysql"`
	Memory   memory.Config `json:"memory"`
}

var DefaultConfig = Config{
	LogLevel: "info",
	Storage:  memory.Name,
}

var (
	logLevelFlag    = flag.String("log-level", DefaultConfig.LogLevel, "(debug, info, warning, error, fatal)")
	configFileFlag  = flag.String("config", "", "Config file path (yaml, json)")
	storageFlag     = flag.String("storage", DefaultConfig.Storage, "Storage backend (mysql, memory)")
	printConfigFlag = flag.Bool("print-config", false, "Print the configuration")
)

func FromFlags() Config {
	cfg := Config{
		LogLevel: *logLevelFlag,
		Storage:  *storageFlag,
		Consul:   consul.ConfigFromFlags(),
		Server:   server.ConfigFromFlags(),
		Mysql:    mysql.ConfigFromFlags(),
		Memory:   memory.ConfigFromFlags(),
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

	if *configFileFlag != "" {
		f, err := os.ReadFile(*configFileFlag)
		if err != nil {
			log.Fatal(err)
		}

		err = yaml.Unmarshal(f, &cfg)
		if err != nil {
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
