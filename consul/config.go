package consul

import "flag"

type Config struct {
	Address               string `json:"address"`
	Token                 string `json:"token"`
	Datacenter            string `json:"datacenter"`
	EnableDistributedLock bool   `json:"enable_distributed_lock"`
	LockPath              string `json:"lock_path"`
}

var DefaultConfig = Config{
	Address:               "localhost:8500",
	Token:                 "",
	Datacenter:            "",
	EnableDistributedLock: false,
	LockPath:              "consul_timeline/lock",
}

var flagConfig Config

func init() {
	flag.StringVar(&flagConfig.Address, "consul", DefaultConfig.Address, "Consul agent address, with an optional http:// or https:// scheme")
	flag.StringVar(&flagConfig.Token, "consul-token", DefaultConfig.Token, "Consul ACL token")
	flag.StringVar(&flagConfig.Datacenter, "consul-datacenter", DefaultConfig.Datacenter, "Datacenter to watch, defaults to the agent's own")
	flag.BoolVar(&flagConfig.EnableDistributedLock, "consul-enable-distributed-lock", DefaultConfig.EnableDistributedLock, "Multi timeline instance lock for storage")
	flag.StringVar(&flagConfig.LockPath, "consul-lock-path", DefaultConfig.LockPath, "Consul lock path")
}

func ConfigFromFlags() Config {
	return flagConfig
}
