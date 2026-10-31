package isureal

import (
	"os"
	"strconv"
)

type Config struct {
	Region       string
	Node         uint8
	CentralAddr  string // 空なら、このノードが順序付け役
	ReplPort     int
	InternalPort int
	HTTPAddr     string
	PackDir      string
	DataDir      string
	PublicDir    string
	AccessLog    string
	Peers        map[uint8]string
}

func ConfigFromEnv() *Config {
	node, _ := strconv.Atoi(envOrDefault("ISUREAL_NODE", "1"))
	replPort, _ := strconv.Atoi(envOrDefault("ISUREAL_REPL_PORT", "8091"))
	internalPort, _ := strconv.Atoi(envOrDefault("ISUREAL_INTERNAL_PORT", "8092"))
	return &Config{
		Region:       envOrDefault("ISUREAL_REGION", "asia"),
		Node:         uint8(node),
		CentralAddr:  os.Getenv("ISUREAL_CENTRAL_ADDR"),
		ReplPort:     replPort,
		InternalPort: internalPort,
		HTTPAddr:     envOrDefault("ISUREAL_HTTP_ADDR", ":8080"),
		PackDir:      envOrDefault("ISUREAL_PACK_DIR", "/home/isucon/pack"),
		DataDir:      envOrDefault("ISUREAL_DATA_DIR", "/home/isucon/isureal-data"),
		PublicDir:    envOrDefault("ISUREAL_PUBLIC_DIR", "/home/isucon/webapp/public"),
		AccessLog:    os.Getenv("ISUREAL_ACCESS_LOG"),
		Peers: map[uint8]string{
			1: "192.168.1.10",
			2: "192.168.2.10",
			3: "192.168.3.10",
			4: "192.168.4.10",
		},
	}
}

func envOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
