package core_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestDefaultDBConfigMatchesDefaultDBUrl(t *testing.T) {
	// the two must not drift apart - DefaultDBUrl is still used as a
	// standalone constant in the docs and tests
	if dsn := core.DefaultDBConfig().DSN(); dsn != core.DefaultDBUrl {
		t.Fatalf("expected %q, got %q", core.DefaultDBUrl, dsn)
	}
}

func TestDBConfigDSN(t *testing.T) {
	scenarios := []struct {
		name     string
		config   core.DBConfig
		expected string
	}{
		{
			"defaults",
			core.DefaultDBConfig(),
			"postgres://pocketbase:pocketbase@localhost:5432/pocketbase?sslmode=disable",
		},
		{
			"custom host and port",
			core.DBConfig{Host: "db.internal", Port: 6543, User: "app", Password: "p", DBName: "app", SSLMode: "require"},
			"postgres://app:p@db.internal:6543/app?sslmode=require",
		},
		{
			"special characters are escaped",
			core.DBConfig{Host: "localhost", Port: 5432, User: "a b", Password: "p@ss:w/rd", DBName: "d", SSLMode: "disable"},
			"postgres://a%20b:p%40ss%3Aw%2Frd@localhost:5432/d?sslmode=disable",
		},
		{
			"empty password",
			core.DBConfig{Host: "localhost", Port: 5432, User: "app", DBName: "d", SSLMode: "disable"},
			"postgres://app@localhost:5432/d?sslmode=disable",
		},
		{
			"ipv6 host",
			core.DBConfig{Host: "::1", Port: 5432, User: "app", DBName: "d", SSLMode: "disable"},
			"postgres://app@[::1]:5432/d?sslmode=disable",
		},
		{
			"explicit url wins over the fields",
			core.DBConfig{Host: "ignored", Port: 1, User: "ignored", DBName: "ignored", Url: "postgres://u:p@h:1/d"},
			"postgres://u:p@h:1/d",
		},
	}

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			if dsn := s.config.DSN(); dsn != s.expected {
				t.Fatalf("expected %q, got %q", s.expected, dsn)
			}
		})
	}
}

func TestDBConfigRedactedDSN(t *testing.T) {
	scenarios := []struct {
		name     string
		config   core.DBConfig
		expected string
	}{
		{
			"masks the password",
			core.DefaultDBConfig(),
			"postgres://pocketbase:***@localhost:5432/pocketbase?sslmode=disable",
		},
		{
			"masks the password of an explicit url",
			core.DBConfig{Url: "postgres://u:secret@h:5432/d"},
			"postgres://u:***@h:5432/d",
		},
		{
			"nothing to mask",
			core.DBConfig{Host: "h", Port: 5432, User: "u", DBName: "d", SSLMode: "disable"},
			"postgres://u@h:5432/d?sslmode=disable",
		},
	}

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			redacted := s.config.RedactedDSN()
			if redacted != s.expected {
				t.Fatalf("expected %q, got %q", s.expected, redacted)
			}
			if strings.Contains(redacted, "secret") || strings.Contains(redacted, ":pocketbase@") {
				t.Fatalf("the password leaked into %q", redacted)
			}
		})
	}
}

func TestDBConfigValidate(t *testing.T) {
	scenarios := []struct {
		name        string
		config      core.DBConfig
		expectError bool
	}{
		{"defaults", core.DefaultDBConfig(), false},
		{"missing host", core.DBConfig{Port: 5432, User: "u", DBName: "d"}, true},
		{"missing user", core.DBConfig{Host: "h", Port: 5432, DBName: "d"}, true},
		{"missing dbName", core.DBConfig{Host: "h", Port: 5432, User: "u"}, true},
		{"port too low", core.DBConfig{Host: "h", Port: 0, User: "u", DBName: "d"}, true},
		{"port too high", core.DBConfig{Host: "h", Port: 70000, User: "u", DBName: "d"}, true},
		{"invalid sslMode", core.DBConfig{Host: "h", Port: 5432, User: "u", DBName: "d", SSLMode: "nope"}, true},
		{"valid sslMode", core.DBConfig{Host: "h", Port: 5432, User: "u", DBName: "d", SSLMode: "verify-full"}, false},
		{"url skips the field checks", core.DBConfig{Url: "postgres://u@h:5432/d"}, false},
	}

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			err := s.config.Validate()
			if s.expectError && err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !s.expectError && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestLoadAndSaveDBConfig(t *testing.T) {
	dataDir := t.TempDir()

	// missing file falls back to the defaults
	config, exists, err := core.LoadDBConfig(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("expected the config file to not exist")
	}
	if config.DSN() != core.DefaultDBUrl {
		t.Fatalf("expected the defaults, got %q", config.DSN())
	}

	config.Host = "db.internal"
	config.Port = 6543
	if err := core.SaveDBConfig(dataDir, config); err != nil {
		t.Fatal(err)
	}

	loaded, exists, err := core.LoadDBConfig(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("expected the config file to exist")
	}
	if loaded.Host != "db.internal" || loaded.Port != 6543 {
		t.Fatalf("unexpected roundtrip result: %#v", loaded)
	}

	// the file holds a plaintext password, so it must not be world readable
	//
	// note: Go only maps the read-only bit on Windows, so the mode is not
	// enforceable there - see the SaveDBConfig docs
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(core.DBConfigPath(dataDir)); err != nil {
			t.Fatal(err)
		} else if perm := info.Mode().Perm(); perm&0077 != 0 {
			t.Fatalf("expected owner-only permissions, got %v", perm)
		}
	}
}

func TestLoadDBConfigPartialFile(t *testing.T) {
	dataDir := t.TempDir()

	// only the host is specified - everything else must fall back
	if err := os.WriteFile(core.DBConfigPath(dataDir), []byte(`{"host":"example.com"}`), 0600); err != nil {
		t.Fatal(err)
	}

	config, _, err := core.LoadDBConfig(dataDir)
	if err != nil {
		t.Fatal(err)
	}

	if config.Host != "example.com" {
		t.Fatalf("expected the stored host, got %q", config.Host)
	}
	if config.Port != core.DefaultDBPort || config.User != core.DefaultDBUser {
		t.Fatalf("expected the defaults to fill in, got %#v", config)
	}
}

func TestLoadDBConfigInvalidFile(t *testing.T) {
	scenarios := []struct {
		name    string
		content string
	}{
		{"malformed json", `{"host":`},
		{"fails validation", `{"port": 99999}`},
	}

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			dataDir := t.TempDir()
			if err := os.WriteFile(core.DBConfigPath(dataDir), []byte(s.content), 0600); err != nil {
				t.Fatal(err)
			}

			// must be reported rather than silently replaced with the defaults,
			// which would quietly connect to the wrong database
			if _, _, err := core.LoadDBConfig(dataDir); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}

func TestResolveDBUrlPrecedence(t *testing.T) {
	dataDir := t.TempDir()

	fileConfig := core.DefaultDBConfig()
	fileConfig.Host = "from-file"
	if err := core.SaveDBConfig(dataDir, fileConfig); err != nil {
		t.Fatal(err)
	}

	t.Run("explicit wins over everything", func(t *testing.T) {
		t.Setenv("PB_DB_URL", "postgres://u@from-env:5432/d")

		dsn, source, err := core.ResolveDBUrl(dataDir, "postgres://u@explicit:5432/d")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dsn, "explicit") || source != core.DBUrlSourceExplicit {
			t.Fatalf("got %q from %q", dsn, source)
		}
	})

	t.Run("env wins over the file", func(t *testing.T) {
		t.Setenv("PB_DB_URL", "postgres://u@from-env:5432/d")

		dsn, source, err := core.ResolveDBUrl(dataDir, "")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dsn, "from-env") || source != core.DBUrlSourceEnv {
			t.Fatalf("got %q from %q", dsn, source)
		}
	})

	t.Run("file wins over the defaults", func(t *testing.T) {
		t.Setenv("PB_DB_URL", "")

		dsn, source, err := core.ResolveDBUrl(dataDir, "")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dsn, "from-file") || source != core.DBUrlSourceFile {
			t.Fatalf("got %q from %q", dsn, source)
		}
	})

	t.Run("defaults when nothing is set", func(t *testing.T) {
		t.Setenv("PB_DB_URL", "")

		dsn, source, err := core.ResolveDBUrl(filepath.Join(t.TempDir(), "empty"), "")
		if err != nil {
			t.Fatal(err)
		}
		if dsn != core.DefaultDBUrl || source != core.DBUrlSourceDefault {
			t.Fatalf("got %q from %q", dsn, source)
		}
	})
}
