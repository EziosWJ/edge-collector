package mqtt

import (
	"crypto/aes"
	"crypto/cipher"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

var (
	ErrMasterSecretRequired = errors.New("MQTT master secret is not configured")
	ErrSecretCiphertext     = errors.New("MQTT secret ciphertext is invalid")
)

// SecretBox encrypts deployment secrets before they cross the persistence
// boundary. The master key never belongs to a database model or API DTO.
type SecretBox struct {
	key [32]byte
}

func NewSecretBox(masterSecret string) (*SecretBox, error) {
	masterSecret = strings.TrimSpace(masterSecret)
	if masterSecret == "" {
		return nil, ErrMasterSecretRequired
	}
	return &SecretBox{key: sha256.Sum256([]byte(masterSecret))}, nil
}

// NewEnvironmentSecretBox reads the deployment-level secret from an
// environment variable or a file with no group/other permissions. The
// APP_ form is used by the application's standard configuration convention;
// the short form is convenient for deployment managers.
func NewEnvironmentSecretBox() (*SecretBox, error) {
	for _, key := range []string{"APP_MQTT__MASTER_SECRET", "MQTT_MASTER_SECRET"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return NewSecretBox(value)
		}
	}
	for _, key := range []string{"APP_MQTT__MASTER_SECRET_FILE", "MQTT_MASTER_SECRET_FILE"} {
		path := strings.TrimSpace(os.Getenv(key))
		if path == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("read MQTT master secret file: %w", err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			return nil, errors.New("MQTT master secret file must not be readable by group or other users")
		}
		value, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read MQTT master secret file: %w", err)
		}
		return NewSecretBox(string(value))
	}
	return nil, ErrMasterSecretRequired
}

func (s *SecretBox) Encrypt(value string) (string, error) {
	if s == nil {
		return "", ErrMasterSecretRequired
	}
	block, err := aes.NewCipher(s.key[:])
	if err != nil {
		return "", fmt.Errorf("create MQTT secret cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create MQTT secret AEAD: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(cryptorand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate MQTT secret nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(value), nil)
	encode := base64.RawURLEncoding
	return "v1." + encode.EncodeToString(nonce) + "." + encode.EncodeToString(ciphertext), nil
}

func (s *SecretBox) Decrypt(value string) (string, error) {
	if s == nil {
		return "", ErrMasterSecretRequired
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 || parts[0] != "v1" {
		return "", ErrSecretCiphertext
	}
	decode := base64.RawURLEncoding
	nonce, err := decode.DecodeString(parts[1])
	if err != nil {
		return "", ErrSecretCiphertext
	}
	ciphertext, err := decode.DecodeString(parts[2])
	if err != nil {
		return "", ErrSecretCiphertext
	}
	block, err := aes.NewCipher(s.key[:])
	if err != nil {
		return "", fmt.Errorf("create MQTT secret cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(nonce) != gcm.NonceSize() {
		return "", ErrSecretCiphertext
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", ErrSecretCiphertext
	}
	return string(plaintext), nil
}
