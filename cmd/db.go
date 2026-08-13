package cmd

import (
	"fmt"
	"strings"

	"github.com/fatih/color"
	"github.com/pocketbase/pocketbase/core"
	"github.com/spf13/cobra"
)

// SkipBootstrapAnnotation marks a command that must run without bootstrapping
// the app.
//
// The db commands exist precisely to fix an unreachable database, so they
// cannot require a working connection first.
const SkipBootstrapAnnotation = "pbSkipBootstrap"

var skipBootstrapAnnotations = map[string]string{SkipBootstrapAnnotation: "true"}

// NewDBCommand creates and returns a new command for inspecting and changing
// the database connection settings.
func NewDBCommand(app core.App) *cobra.Command {
	command := &cobra.Command{
		Use:         "db",
		Short:       "Inspect and change the database connection",
		Annotations: skipBootstrapAnnotations,
	}

	command.AddCommand(dbShowCommand(app))
	command.AddCommand(dbSetCommand(app))
	command.AddCommand(dbTestCommand(app))

	return command
}

func dbShowCommand(app core.App) *cobra.Command {
	return &cobra.Command{
		Use:          "show",
		Short:        "Prints the connection currently in use (password masked)",
		SilenceUsage: true,
		Annotations:  skipBootstrapAnnotations,
		RunE: func(command *cobra.Command, args []string) error {
			dsn, source, err := core.ResolveDBUrl(app.DataDir(), "")
			if err != nil {
				return err
			}

			config, exists, err := core.LoadDBConfig(app.DataDir())
			if err != nil {
				return err
			}

			color.HiBlack("config file: %s", core.DBConfigPath(app.DataDir()))
			if !exists {
				color.HiBlack("             (not created yet - using the built-in defaults)")
			}
			fmt.Println()

			fmt.Printf("connection: %s\n", color.CyanString(redactedDSN(dsn)))
			fmt.Printf("source:     %s\n", color.CyanString(source))

			// the individual parts are only meaningful when they are what
			// actually produced the connection string
			if source == core.DBUrlSourceFile || source == core.DBUrlSourceDefault {
				fmt.Println()
				if config.Url != "" {
					fmt.Printf("  url:      %s\n", redactedDSN(config.Url))
				} else {
					fmt.Printf("  host:     %s\n", config.Host)
					fmt.Printf("  port:     %d\n", config.Port)
					fmt.Printf("  user:     %s\n", config.User)
					fmt.Printf("  password: %s\n", maskedPassword(config.Password))
					fmt.Printf("  dbName:   %s\n", config.DBName)
					fmt.Printf("  sslMode:  %s\n", config.SSLMode)
				}
			} else {
				fmt.Println()
				color.HiBlack("note: %s takes precedence over %s", source, core.DBConfigFileName)
			}

			// resolved independently of how the connection itself resolved
			fmt.Println()
			fmt.Println("backup tools:")
			fmt.Printf("  pg_dump:  %s\n", strings.Join(core.PgDumpCommand(app.DataDir()), " "))
			fmt.Printf("  psql:     %s\n", strings.Join(core.PgRestoreCommand(app.DataDir()), " "))

			return nil
		},
	}
}

func dbSetCommand(app core.App) *cobra.Command {
	var host, user, password, dbName, sslMode, rawUrl, pgDump, psql string
	var port int
	var clearUrl bool

	command := &cobra.Command{
		Use:   "set",
		Short: "Changes the stored database connection settings",
		Long: "Changes the stored database connection settings.\n\n" +
			"Only the provided flags are updated; everything else is left as is.\n" +
			"The values are written to " + core.DBConfigFileName + " in the data directory,\n" +
			"which includes the password in plain text - prefer PB_DB_URL when you have\n" +
			"a secret store available.",
		Example: "db set --host db.internal --port 5432 --user app --password secret --dbName app",
		// without at least one flag the command would silently do nothing
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Annotations:  skipBootstrapAnnotations,
		RunE: func(command *cobra.Command, args []string) error {
			config, _, err := core.LoadDBConfig(app.DataDir())
			if err != nil {
				return err
			}

			changed := false
			flags := command.Flags()

			// only touch what the user actually passed, so that a partial
			// update does not reset the other fields to their defaults
			for name, apply := range map[string]func(){
				"host":     func() { config.Host = host },
				"port":     func() { config.Port = port },
				"user":     func() { config.User = user },
				"password": func() { config.Password = password },
				"dbName":   func() { config.DBName = dbName },
				"sslMode":  func() { config.SSLMode = sslMode },
				"url":      func() { config.Url = rawUrl },
				"pgDump":   func() { config.PgDump = pgDump },
				"psql":     func() { config.Psql = psql },
			} {
				if flags.Changed(name) {
					apply()
					changed = true
				}
			}

			if clearUrl {
				config.Url = ""
				changed = true
			}

			if !changed {
				return fmt.Errorf("nothing to change - pass at least one flag (see %q)", "db set --help")
			}

			if err := core.SaveDBConfig(app.DataDir(), config); err != nil {
				return err
			}

			color.Green("Saved %s", core.DBConfigPath(app.DataDir()))
			fmt.Printf("connection: %s\n", color.CyanString(redactedDSN(config.DSN())))

			if _, source, err := core.ResolveDBUrl(app.DataDir(), ""); err == nil && source != core.DBUrlSourceFile {
				fmt.Println()
				color.Yellow("warning: %s currently takes precedence, so the saved values are not in use", source)
			}

			fmt.Println()
			color.HiBlack("run %q to verify the connection", "db test")

			return nil
		},
	}

	command.Flags().StringVar(&host, "host", core.DefaultDBHost, "database host")
	command.Flags().IntVar(&port, "port", core.DefaultDBPort, "database port")
	command.Flags().StringVar(&user, "user", core.DefaultDBUser, "database user")
	command.Flags().StringVar(&password, "password", "", "database password")
	command.Flags().StringVar(&dbName, "dbName", core.DefaultDBName, "database name")
	command.Flags().StringVar(&sslMode, "sslMode", core.DefaultDBSSLMode,
		"sslmode: disable, allow, prefer, require, verify-ca or verify-full")
	command.Flags().StringVar(&rawUrl, "url", "",
		"full connection string, used verbatim and overriding every other field")
	command.Flags().BoolVar(&clearUrl, "clearUrl", false, "removes a previously stored --url")
	command.Flags().StringVar(&pgDump, "pgDump", "",
		`command used to create backups (default "pg_dump" from PATH), `+
			`eg. "docker exec -i pocketbase-postgres pg_dump"`)
	command.Flags().StringVar(&psql, "psql", "",
		`command used to restore backups (default "psql" from PATH), `+
			`eg. "docker exec -i pocketbase-postgres psql"`)

	return command
}

func dbTestCommand(app core.App) *cobra.Command {
	return &cobra.Command{
		Use:          "test",
		Short:        "Verifies that the database is reachable",
		SilenceUsage: true,
		Annotations:  skipBootstrapAnnotations,
		RunE: func(command *cobra.Command, args []string) error {
			dsn, source, err := core.ResolveDBUrl(app.DataDir(), "")
			if err != nil {
				return err
			}

			fmt.Printf("connecting to %s (from %s)...\n", color.CyanString(redactedDSN(dsn)), source)

			db, err := core.DefaultDBConnect(dsn)
			if err != nil {
				return err
			}
			defer db.Close()

			if err := db.DB().Ping(); err != nil {
				return fmt.Errorf("connection failed: %w", err)
			}

			var version string
			if err := db.NewQuery("SELECT version()").Row(&version); err != nil {
				return fmt.Errorf("connected, but the test query failed: %w", err)
			}

			color.Green("OK")
			color.HiBlack("%s", version)

			return nil
		},
	}
}

func redactedDSN(dsn string) string {
	return core.DBConfig{Url: dsn}.RedactedDSN()
}

func maskedPassword(password string) string {
	if password == "" {
		return "(empty)"
	}

	return "***"
}
