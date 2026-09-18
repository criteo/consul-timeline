package mysql

import (
	"flag"
)

const Name = "mysql"

type Config struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	Database string `json:"database"`

	SetupSchema bool `json:"setup_schema"`
	PrintSchema bool `json:"-"`

	// RetentionDays bounds the history kept: older daily partitions are
	// dropped, instances and rollups pruned.
	RetentionDays int `json:"retention_days"`
	// FacetSample caps the number of most recent matching rows scanned for
	// facets and filtered histograms.
	FacetSample  int `json:"facet_sample"`
	MaxOpenConns int `json:"max_open_conns"`
	// Params are extra driver parameters appended to the connection string,
	// for example "tls=skip-verify&allowCleartextPasswords=true" when the
	// server authenticates users through PAM.
	Params string `json:"params"`
}

var DefaultConfig = Config{
	Host:          "localhost",
	Port:          3306,
	User:          "",
	Password:      "",
	Database:      "consul_timeline",
	SetupSchema:   false,
	PrintSchema:   false,
	RetentionDays: 14,
	FacetSample:   100000,
	MaxOpenConns:  16,
}

var flagConfig Config

func init() {
	flag.BoolVar(&flagConfig.SetupSchema, "mysql-setup-schema", DefaultConfig.SetupSchema, "Create the MySQL schema at startup")
	flag.BoolVar(&flagConfig.PrintSchema, "mysql-print-schema", DefaultConfig.PrintSchema, "Print the MySQL schema and exit")

	flag.StringVar(&flagConfig.Host, "mysql-host", DefaultConfig.Host, "MySQL server host")
	flag.IntVar(&flagConfig.Port, "mysql-port", DefaultConfig.Port, "MySQL server port")
	flag.StringVar(&flagConfig.User, "mysql-user", DefaultConfig.User, "MySQL user")
	flag.StringVar(&flagConfig.Password, "mysql-password", DefaultConfig.Password, "MySQL password")
	flag.StringVar(&flagConfig.Database, "mysql-db", DefaultConfig.Database, "MySQL database name")

	flag.IntVar(&flagConfig.RetentionDays, "mysql-retention-days", DefaultConfig.RetentionDays, "Days of history to keep")
	flag.IntVar(&flagConfig.FacetSample, "mysql-facet-sample", DefaultConfig.FacetSample, "Most recent matching rows scanned for facets and filtered histograms")
	flag.IntVar(&flagConfig.MaxOpenConns, "mysql-max-open-conns", DefaultConfig.MaxOpenConns, "Connection pool size")
	flag.StringVar(&flagConfig.Params, "mysql-params", DefaultConfig.Params, "Extra driver parameters appended to the connection string (e.g. tls=skip-verify&allowCleartextPasswords=true)")
}

func ConfigFromFlags() Config {
	return flagConfig
}
