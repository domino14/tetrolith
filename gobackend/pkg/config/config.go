package config

import (
	"github.com/namsral/flag"
)

type Config struct {
	Debug               bool
	WebsocketAddress    string
	SecretKey           string
	WordDBServerAddress string
}

// Load loads the configs from the given arguments
func (c *Config) Load(args []string) error {
	fs := flag.NewFlagSet("liwords-socket", flag.ContinueOnError)

	fs.StringVar(&c.WebsocketAddress, "ws-address", ":8087", "WS server listens on this address")
	fs.BoolVar(&c.Debug, "debug", false, "debug logging on")
	fs.StringVar(&c.SecretKey, "secret-key", "", "secret key must be a random unguessable string")
	fs.StringVar(&c.WordDBServerAddress, "word-db-server-address", "", "address for word db server")
	err := fs.Parse(args)
	return err
}
