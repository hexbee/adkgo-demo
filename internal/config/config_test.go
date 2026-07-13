package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"BASE_URL", "API_KEY", "MODEL_NAME", "CONTEXT_WINDOW", "MAX_TOKENS"} {
		value, existed := os.LookupEnv(name)
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if existed {
				_ = os.Setenv(name, value)
			} else {
				_ = os.Unsetenv(name)
			}
		})
	}
}

func writeEnv(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadReadsDotEnv(t *testing.T) {
	clearConfigEnv(t)
	path := writeEnv(t, "BASE_URL=https://example.test/v1\nAPI_KEY=secret-value\nMODEL_NAME=test-model\nCONTEXT_WINDOW=1000000\nMAX_TOKENS=384000\n")
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseURL != "https://example.test/v1" || got.ModelName != "test-model" {
		t.Fatalf("unexpected config: %+v", got)
	}
	if strings.Contains(got.SafeSummary(), "secret-value") {
		t.Fatal("safe summary leaked API key")
	}
}

func TestLoadProcessEnvironmentWins(t *testing.T) {
	clearConfigEnv(t)
	path := writeEnv(t, "BASE_URL=https://file.test/v1\nAPI_KEY=file-key\nMODEL_NAME=file-model\nCONTEXT_WINDOW=100\nMAX_TOKENS=50\n")
	t.Setenv("MODEL_NAME", "shell-model")
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.ModelName != "shell-model" {
		t.Fatalf("model = %q", got.ModelName)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"missing key", "BASE_URL=https://x.test/v1\nMODEL_NAME=m\nCONTEXT_WINDOW=10\nMAX_TOKENS=5\n", "API_KEY"},
		{"bad URL", "BASE_URL=not-a-url\nAPI_KEY=k\nMODEL_NAME=m\nCONTEXT_WINDOW=10\nMAX_TOKENS=5\n", "BASE_URL"},
		{"bad context", "BASE_URL=https://x.test/v1\nAPI_KEY=k\nMODEL_NAME=m\nCONTEXT_WINDOW=0\nMAX_TOKENS=5\n", "CONTEXT_WINDOW"},
		{"bad max", "BASE_URL=https://x.test/v1\nAPI_KEY=k\nMODEL_NAME=m\nCONTEXT_WINDOW=10\nMAX_TOKENS=-1\n", "MAX_TOKENS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearConfigEnv(t)
			_, err := Load(writeEnv(t, tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}
