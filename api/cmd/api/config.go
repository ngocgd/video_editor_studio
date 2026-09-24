package main

import "time"

// config holds the API process settings, loaded via caarlos0/env.
type config struct {
	Addr            string        `env:"API_ADDR" envDefault:":8080"`
	LogLevel        string        `env:"API_LOG_LEVEL" envDefault:"info"`
	ShutdownTimeout time.Duration `env:"API_SHUTDOWN_TIMEOUT" envDefault:"25s"`
	DatabaseURL     string        `env:"DATABASE_URL,required"`
	Version         string        `env:"API_VERSION" envDefault:"dev"`
}
