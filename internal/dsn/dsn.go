// Package dsn assembles Postgres connection strings from individual components.
// Building the DSN in one place via url.UserPassword guarantees the password is
// percent-encoded, so credentials containing URL-reserved characters (which
// RDS-managed passwords routinely include) never break url.Parse in pgx or
// golang-migrate.
package dsn

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// Components are the parts of a Postgres connection string.
type Components struct {
	User     string
	Password string
	Host     string
	Port     string
	Name     string
	SSLMode  string
}

// DSN renders the components as a postgres:// URL. url.UserPassword percent-
// encodes the userinfo, so any password parses cleanly when read back.
func (c Components) DSN() string {
	host := c.Host
	if c.Port != "" {
		host = net.JoinHostPort(c.Host, c.Port)
	}
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.User, c.Password),
		Host:   host,
		Path:   "/" + c.Name,
	}
	if c.SSLMode != "" {
		u.RawQuery = url.Values{"sslmode": {c.SSLMode}}.Encode()
	}
	return u.String()
}

// FromEnv reads components from <prefix>USER, <prefix>PASSWORD, <prefix>HOST,
// <prefix>PORT, <prefix>NAME, <prefix>SSLMODE. USER, PASSWORD, HOST and NAME are
// required; PORT defaults to "5432"; SSLMODE is optional. It returns an error
// naming every missing required variable.
func FromEnv(prefix string) (Components, error) {
	c := Components{
		User:     os.Getenv(prefix + "USER"),
		Password: os.Getenv(prefix + "PASSWORD"),
		Host:     os.Getenv(prefix + "HOST"),
		Port:     os.Getenv(prefix + "PORT"),
		Name:     os.Getenv(prefix + "NAME"),
		SSLMode:  os.Getenv(prefix + "SSLMODE"),
	}
	var missing []string
	if c.User == "" {
		missing = append(missing, prefix+"USER")
	}
	if c.Password == "" {
		missing = append(missing, prefix+"PASSWORD")
	}
	if c.Host == "" {
		missing = append(missing, prefix+"HOST")
	}
	if c.Name == "" {
		missing = append(missing, prefix+"NAME")
	}
	if len(missing) > 0 {
		return Components{}, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}
	if c.Port == "" {
		c.Port = "5432"
	}
	return c, nil
}
