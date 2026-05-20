package crypto

import (
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

func newKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	_, err := rand.Read(k)
	require.NoError(t, err)
	return k
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	c, err := NewCipher(newKey(t))
	require.NoError(t, err)

	plain := []byte("BQDdK0z...spotify access token...")
	ciphertext, err := c.Encrypt(plain)
	require.NoError(t, err)
	require.NotEqual(t, plain, ciphertext)

	out, err := c.Decrypt(ciphertext)
	require.NoError(t, err)
	require.Equal(t, plain, out)
}

func TestEncrypt_UniqueNoncesProduceDifferentCiphertexts(t *testing.T) {
	c, err := NewCipher(newKey(t))
	require.NoError(t, err)
	a, err := c.Encrypt([]byte("same"))
	require.NoError(t, err)
	b, err := c.Encrypt([]byte("same"))
	require.NoError(t, err)
	require.NotEqual(t, a, b)
}

func TestNewCipher_WrongKeySize(t *testing.T) {
	_, err := NewCipher([]byte("too short"))
	require.Error(t, err)
}

func TestDecrypt_TamperedCiphertextRejected(t *testing.T) {
	c, err := NewCipher(newKey(t))
	require.NoError(t, err)
	ciphertext, err := c.Encrypt([]byte("hello"))
	require.NoError(t, err)
	ciphertext[len(ciphertext)-1] ^= 0xFF
	_, err = c.Decrypt(ciphertext)
	require.Error(t, err)
}
