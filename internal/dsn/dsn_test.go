package dsn

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// A password full of URL-reserved characters (the kind RDS auto-generates) must
// survive a DSN round-trip: assembling then re-parsing yields the exact password.
// This is the regression test for the "invalid IP-literal" prod migration bug.
func TestDSN_ReservedCharPasswordRoundTrips(t *testing.T) {
	c := Components{
		User:     "app",
		Password: "[kkH>6KvYXOHla15:FRkin#z?x",
		Host:     "db.example.com",
		Port:     "5432",
		Name:     "appdb",
		SSLMode:  "require",
	}
	u, err := url.Parse(c.DSN())
	require.NoError(t, err)
	pw, ok := u.User.Password()
	require.True(t, ok)
	require.Equal(t, c.Password, pw)
	require.Equal(t, "db.example.com:5432", u.Host)
	require.Equal(t, "/appdb", u.Path)
	require.Equal(t, "require", u.Query().Get("sslmode"))
}

func TestDSN_OmitsSSLModeWhenEmpty(t *testing.T) {
	c := Components{User: "app", Password: "pw", Host: "localhost", Port: "5432", Name: "appdb"}
	require.Equal(t, "postgres://app:pw@localhost:5432/appdb", c.DSN())
}

func TestDSN_OmitsPortWhenEmpty(t *testing.T) {
	c := Components{User: "app", Password: "pw", Host: "localhost", Name: "appdb"}
	require.Equal(t, "postgres://app:pw@localhost/appdb", c.DSN())
}

func TestFromEnv_AssemblesAndDefaultsPort(t *testing.T) {
	t.Setenv("DB_USER", "app")
	t.Setenv("DB_PASSWORD", "pw")
	t.Setenv("DB_HOST", "localhost")
	t.Setenv("DB_NAME", "appdb")
	t.Setenv("DB_PORT", "")    // unset -> defaults to 5432
	t.Setenv("DB_SSLMODE", "") // unset -> omitted
	c, err := FromEnv("DB_")
	require.NoError(t, err)
	require.Equal(t, "5432", c.Port)
	require.Equal(t, "postgres://app:pw@localhost:5432/appdb", c.DSN())
}

func TestFromEnv_MissingRequiredListsThem(t *testing.T) {
	t.Setenv("DB_USER", "")
	t.Setenv("DB_PASSWORD", "")
	t.Setenv("DB_HOST", "")
	t.Setenv("DB_NAME", "")
	_, err := FromEnv("DB_")
	require.Error(t, err)
	require.Contains(t, err.Error(), "DB_USER")
	require.Contains(t, err.Error(), "DB_PASSWORD")
	require.Contains(t, err.Error(), "DB_HOST")
	require.Contains(t, err.Error(), "DB_NAME")
}
