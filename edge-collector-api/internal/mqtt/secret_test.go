package mqtt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewEnvironmentSecretBoxRequiresReadOnlyOwnerFile(t *testing.T) {
	for _, key := range []string{
		"APP_MQTT__MASTER_SECRET", "MQTT_MASTER_SECRET",
		"APP_MQTT__MASTER_SECRET_FILE", "MQTT_MASTER_SECRET_FILE",
	} {
		t.Setenv(key, "")
	}
	path := filepath.Join(t.TempDir(), "master-secret")
	if err := os.WriteFile(path, []byte("file-master-secret"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MQTT_MASTER_SECRET_FILE", path)
	box, err := NewEnvironmentSecretBox()
	if err != nil {
		t.Fatalf("read owner-only secret file: %v", err)
	}
	if _, err := box.Encrypt("round-trip"); err != nil {
		t.Fatalf("encrypt with file secret: %v", err)
	}

	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewEnvironmentSecretBox(); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("writable secret file error = %v", err)
	}
}
