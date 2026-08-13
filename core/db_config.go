package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// DBConfigFileName is the name of the connection config file stored inside the
// data directory.
const DBConfigFileName = "db.json"

// Built-in connection defaults. They match the "postgres" service of the
// bundled docker-compose.yml so that a fresh checkout runs without any setup.
const (
	DefaultDBHost     = "localhost"
	DefaultDBPort     = 5432
	DefaultDBUser     = "pocketbase"
	DefaultDBPassword = "pocketbase"
	DefaultDBName     = "pocketbase"
	DefaultDBSSLMode  = "disable"
)

// validSSLModes are the sslmode values accepted by libpq/pgx.
var validSSLModes = []string{
	"disable", "allow", "prefer", "require", "verify-ca", "verify-full",
}

// DBConfig holds the individual parts of the Postgres connection.
//
// It is persisted as JSON in <dataDir>/db.json and sits below the --dbUrl flag
// and the PB_DB_URL env variable in the resolution order (see [ResolveDBUrl]).
type DBConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	DBName   string `json:"dbName"`
	SSLMode  string `json:"sslMode"`

	// Url, when set, is used verbatim and every other field is ignored.
	//
	// It is an escape hatch for connection strings that cannot be expressed
	// with the fields above (eg. client certificates or extra libpq params).
	Url string `json:"url,omitempty"`

	// PgDump and Psql override the commands used by the backup feature.
	//
	// They are stored here rather than only read from the environment because
	// backups are usually triggered from the admin UI of an already running
	// server, long after any env variable could have been set. The typical
	// value runs the tool inside the Postgres container:
	//
	//	"docker exec -i pocketbase-postgres pg_dump"
	PgDump string `json:"pgDump,omitempty"`
	Psql   string `json:"psql,omitempty"`
}

// DefaultDBConfig returns the built-in connection defaults.
func DefaultDBConfig() DBConfig {
	return DBConfig{
		Host:     DefaultDBHost,
		Port:     DefaultDBPort,
		User:     DefaultDBUser,
		Password: DefaultDBPassword,
		DBName:   DefaultDBName,
		SSLMode:  DefaultDBSSLMode,
	}
}

// Validate checks that the config can produce a usable connection string.
func (c DBConfig) Validate() error {
	if c.Url != "" {
		if _, err := url.Parse(c.Url); err != nil {
			return fmt.Errorf("invalid url: %w", err)
		}

		return nil
	}

	if c.Host == "" {
		return errors.New("host is required")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", c.Port)
	}
	if c.User == "" {
		return errors.New("user is required")
	}
	if c.DBName == "" {
		return errors.New("dbName is required")
	}
	if c.SSLMode != "" && !slices.Contains(validSSLModes, c.SSLMode) {
		return fmt.Errorf("invalid sslMode %q (must be one of %v)", c.SSLMode, validSSLModes)
	}

	return nil
}

// DSN builds the Postgres connection string described by the config.
func (c DBConfig) DSN() string {
	return c.dsn(c.Password)
}

// passwordMask is what a redacted password is displayed as.
const passwordMask = "***"

// passwordMaskSentinel is substituted before the mask because net/url would
// percent-encode the mask characters into an unreadable "%2A%2A%2A".
const passwordMaskSentinel = "pbRedactedPassword"

// RedactedDSN is like [DBConfig.DSN] but with the password masked, so that it
// is safe to print.
func (c DBConfig) RedactedDSN() string {
	if c.Url != "" {
		return redactUrlPassword(c.Url)
	}

	if c.Password == "" {
		return c.dsn("")
	}

	return strings.Replace(c.dsn(passwordMaskSentinel), passwordMaskSentinel, passwordMask, 1)
}

func (c DBConfig) dsn(password string) string {
	if c.Url != "" {
		return c.Url
	}

	u := &url.URL{
		Scheme: "postgres",
		Host:   net.JoinHostPort(c.Host, strconv.Itoa(c.Port)),
		Path:   "/" + c.DBName,
	}

	if password != "" {
		u.User = url.UserPassword(c.User, password)
	} else {
		u.User = url.User(c.User)
	}

	if c.SSLMode != "" {
		u.RawQuery = url.Values{"sslmode": []string{c.SSLMode}}.Encode()
	}

	return u.String()
}

// redactUrlPassword masks the password of an already assembled connection string.
func redactUrlPassword(rawUrl string) string {
	parsed, err := url.Parse(rawUrl)
	if err != nil || parsed.User == nil {
		return rawUrl
	}

	if _, hasPassword := parsed.User.Password(); !hasPassword {
		return rawUrl
	}

	parsed.User = url.UserPassword(parsed.User.Username(), passwordMaskSentinel)

	return strings.Replace(parsed.String(), passwordMaskSentinel, passwordMask, 1)
}

// DBConfigPath returns the path of the connection config file.
func DBConfigPath(dataDir string) string {
	return filepath.Join(dataDir, DBConfigFileName)
}

// LoadDBConfig reads the connection config from the data directory.
//
// Missing fields fall back to the built-in defaults, so a partial file is
// valid. The second return value reports whether the file existed at all.
func LoadDBConfig(dataDir string) (DBConfig, bool, error) {
	config := DefaultDBConfig()

	raw, err := os.ReadFile(DBConfigPath(dataDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return config, false, nil
		}

		return config, false, err
	}

	if err := json.Unmarshal(raw, &config); err != nil {
		return config, true, fmt.Errorf("failed to parse %s: %w", DBConfigPath(dataDir), err)
	}

	if err := config.Validate(); err != nil {
		return config, true, fmt.Errorf("invalid %s: %w", DBConfigPath(dataDir), err)
	}

	return config, true, nil
}

// SaveDBConfig writes the connection config into the data directory.
//
// The file contains the database password in plain text, so it is written with
// owner-only permissions.
//
// Note that Go only maps the read-only bit on Windows, so the 0600 mode has no
// effect there and the file stays readable by other users of the machine.
// Prefer PB_DB_URL when a secret store is available.
func SaveDBConfig(dataDir string, config DBConfig) error {
	if err := config.Validate(); err != nil {
		return err
	}

	if err := os.MkdirAll(dataDir, os.ModePerm); err != nil {
		return err
	}

	raw, err := json.MarshalIndent(config, "", "    ")
	if err != nil {
		return err
	}

	return os.WriteFile(DBConfigPath(dataDir), append(raw, '\n'), 0600)
}

// DSN source labels reported by [ResolveDBUrl].
const (
	DBUrlSourceExplicit = "--dbUrl flag"
	DBUrlSourceEnv      = "PB_DB_URL env variable"
	DBUrlSourceFile     = DBConfigFileName
	DBUrlSourceDefault  = "built-in defaults"
)

// newDBConnectionError wraps a connection failure with the connection string
// actually in use (password masked), where it came from, and how to change it.
func (app *BaseApp) newDBConnectionError(cause error) error {
	source := app.dbUrlSource
	if source == "" {
		source = "unknown"
	}

	return fmt.Errorf(
		"failed to connect to Postgres at %s\n"+
			"  connection string source: %s\n"+
			"  resolution order: --dbUrl flag, then PB_DB_URL, then %s, then the built-in defaults\n"+
			"  to change it: pocketbase db set --host <host> --port <port> --user <user> --password <password> --dbName <name>\n"+
			"  underlying error: %w",
		redactUrlPassword(app.config.DBUrl), source, DBConfigFileName, cause,
	)
}

// ResolveDBUrl determines the connection string to use and reports where it
// came from, in descending priority:
//
//  1. explicit (the --dbUrl flag or an app config value)
//  2. the PB_DB_URL env variable
//  3. <dataDir>/db.json
//  4. the built-in defaults
//
// A malformed db.json is reported as an error rather than silently ignored -
// falling back to the defaults would quietly connect to the wrong database.
func ResolveDBUrl(dataDir string, explicit string) (dsn string, source string, err error) {
	if explicit != "" {
		return explicit, DBUrlSourceExplicit, nil
	}

	if env := os.Getenv("PB_DB_URL"); env != "" {
		return env, DBUrlSourceEnv, nil
	}

	config, exists, err := LoadDBConfig(dataDir)
	if err != nil {
		return "", "", err
	}

	if exists {
		return config.DSN(), DBUrlSourceFile, nil
	}

	return config.DSN(), DBUrlSourceDefault, nil
}
