package secrets

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// RDS-managed master secrets store a JSON document; we only need the password.
// The value routinely contains URL-reserved characters, so this must be exact.
func TestParseDBPassword_ExtractsPassword(t *testing.T) {
	raw := []byte(`{"username":"app","password":"[kkH>6KvYXOHla15:FRkin#z?x","engine":"postgres","host":"h","port":5432,"dbname":"appdb"}`)
	pw, err := parseDBPassword(raw)
	require.NoError(t, err)
	require.Equal(t, "[kkH>6KvYXOHla15:FRkin#z?x", pw)
}

func TestParseDBPassword_MissingPassword(t *testing.T) {
	_, err := parseDBPassword([]byte(`{"username":"app"}`))
	require.Error(t, err)
}

func TestParseDBPassword_InvalidJSON(t *testing.T) {
	_, err := parseDBPassword([]byte("not json"))
	require.Error(t, err)
}
