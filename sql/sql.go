package sql

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/magefile/mage/sh"
	"github.com/ttab/mage/ia"
	"github.com/ttab/mage/internal"
)

const (
	sqlTools = "ghcr.io/ttab/elephant-sqltools:v0.1.3"

	postgresImage   = "docker.io/pgvector/pgvector:pg17"
	postgresImage18 = "docker.io/pgvector/pgvector:pg18"
)

// Container names for the local Postgres instances. The 17 instances are named
// after the project that created them, the 18 instance is shared between them
// all, as the per-project data directory was never anything we made use of.
const (
	pg17Prefix   = "postgres-"
	pg18Instance = "postgres18"
)

// SqlcCommand returns a command function that runs sqlc in docker with the
// current working directory mounted.
func SqlcCommand() func(args ...string) error {
	uid := os.Getuid()
	gid := os.Getgid()
	cwd := internal.MustGetWD()

	return sh.RunCmd("docker", "run", "--rm",
		"-v", fmt.Sprintf("%s:/usr/src", cwd),
		"-u", fmt.Sprintf("%d:%d", uid, gid),
		sqlTools, "sqlc",
	)
}

// SqlcCommand returns a command function that runs tern in docker with host
// networking and the current working directory mounted.
func TernCommand() func(args ...string) error {
	cwd := internal.MustGetWD()

	return sh.RunCmd("docker", "run", "--rm",
		"-v", fmt.Sprintf("%s:/usr/src", cwd),
		"--network", "host",
		sqlTools, "tern",
	)
}

// Generate uses sqlc to compile the SQL queries in postgres/queries.sql to Go,
// adding he default sqlc.yaml file if necessary.
func Generate() error {
	hasConfig, err := internal.FileExists("sqlc.yaml")
	if err != nil {
		return fmt.Errorf("check for sqlc config: %w", err)
	}

	if !hasConfig {
		err := SqlcConfig()
		if err != nil {
			return fmt.Errorf("add default config: %w", err)
		}
	}

	sqlc := SqlcCommand()

	err = sqlc("--experimental", "generate")
	if err != nil {
		return fmt.Errorf("sqlc: %w", err)
	}

	return nil
}

//go:embed sqlc.yaml
var defaultSqlcConfig []byte

// SqlcConfig adds the default sqlc config.
func SqlcConfig() error {
	err := os.WriteFile("sqlc.yaml", defaultSqlcConfig, 0o600)
	if err != nil {
		return fmt.Errorf("write sqlc.yaml: %w", err)
	}

	return nil
}

// Migrate the database to the latest version using the migrations in
// "./schema".
func Migrate() error {
	connString := MustGetConnString()
	tern := TernCommand()

	err := tern("migrate", "--migrations", "schema",
		"--conn-string", connString)
	if err != nil {
		return fmt.Errorf("run migration: %w", err)
	}

	err = DumpSchema()
	if err != nil {
		return fmt.Errorf("dump schema after migration: %w", err)
	}

	return nil
}

// Rollback to the specific schema version.
func Rollback(to int) error {
	connString := MustGetConnString()
	tern := TernCommand()

	err := tern("migrate", "--migrations", "schema",
		"--conn-string", connString,
		"--destination", strconv.Itoa(to))
	if err != nil {
		return fmt.Errorf("run migration: %w", err)
	}

	err = DumpSchema()
	if err != nil {
		return fmt.Errorf("dump schema after rollback: %w", err)
	}

	return nil
}

// DumpSchema writes the current database schema to "./postgres/schema.sql".
func DumpSchema() error {
	connString := MustGetConnString()

	outFile, err := os.Create(filepath.Join("postgres", "schema.sql"))
	if err != nil {
		return fmt.Errorf("create schema file: %w", err)
	}

	// Buffer for keeping the dumped schema in memory for postprocessing.
	var buf bytes.Buffer

	// Always dump with the newest pg_dump we ship. It reads older servers
	// fine, so a project still on 17 gets the same schema.sql it will get
	// once it moves to 18.
	ok, err := sh.Exec(nil, &buf, os.Stderr,
		"docker", "run", "--rm", "--network", "host",
		postgresImage18,
		"pg_dump", connString,
		"--schema-only", "--no-owner", "--no-privileges",
	)
	if err != nil {
		return fmt.Errorf("run pg_dump: %w", err)
	}

	if !ok {
		return errors.New("failed to run pg_dump in docker")
	}

	// Scanner that will read the dumped schema line by line.
	scan := bufio.NewScanner(&buf)

	var (
		restrict   = []byte("\\restrict")
		unrestrict = []byte("\\unrestrict")
		dumpedFrom = []byte("-- Dumped from database version")
		dumpedBy   = []byte("-- Dumped by pg_dump version")
		nl         = []byte("\n")
	)

	var writeErr error

	for scan.Scan() {
		line := scan.Bytes()

		// Ignore the \restrict and \unrestrict directives, and the
		// version header, which records the versions of the server and
		// pg_dump that happened to produce the dump. The schema is the
		// same either way, so keeping them just churns the file every
		// time we move to a new Postgres.
		if hasAnyPrefix(line, restrict, unrestrict, dumpedFrom, dumpedBy) {
			continue
		}

		_, writeErr = outFile.Write(line)
		if writeErr != nil {
			break
		}

		_, writeErr = outFile.Write(nl)
		if writeErr != nil {
			break
		}
	}

	if writeErr != nil {
		return fmt.Errorf("write to schema.sql: %w", writeErr)
	}

	readErr := scan.Err()
	if readErr != nil {
		return fmt.Errorf("read from database dump: %w", readErr)
	}

	return nil
}

func hasAnyPrefix(line []byte, prefixes ...[]byte) bool {
	for _, p := range prefixes {
		if bytes.HasPrefix(line, p) {
			return true
		}
	}

	return false
}

// Postgres creates a local Postgres 17 instance using docker.
func Postgres(name string) error {
	return startPostgres(pg17Prefix+name, postgresImage)
}

// Postgres18 creates the local Postgres 18 instance using docker.
func Postgres18() error {
	return startPostgres(pg18Instance, postgresImage18)
}

func startPostgres(instanceName string, image string) error {
	uid := os.Getuid()
	gid := os.Getgid()

	stateDir, err := internal.StateDir()
	if err != nil {
		return fmt.Errorf("get state directory path: %w", err)
	}

	dataDir := filepath.Join(stateDir, instanceName)

	err = os.MkdirAll(dataDir, 0o700)
	if err != nil {
		return fmt.Errorf("create local state directory: %w", err)
	}

	err = stopRunningInstances()
	if err != nil {
		return err
	}

	err = sh.Run("docker", "run", "-d", "--rm",
		"--name", instanceName,
		"--user", fmt.Sprintf("%d:%d", uid, gid),
		"-e", "POSTGRES_USER=admin",
		"-e", "POSTGRES_PASSWORD=pass",
		"-e", "PGDATA=/var/lib/postgresql/data/pgdata",
		"-v", fmt.Sprintf("%s:/var/lib/postgresql/data", dataDir),
		"-p", "5432:5432",
		image,
		"-c", "wal_level=logical",
		"-c", "log_lock_waits=on",
	)
	if err != nil {
		return fmt.Errorf("start postgres: %w", err)
	}

	return nil
}

// stopRunningInstances stops all the Postgres instances we manage. They all
// publish port 5432, and no major version can read another's data directory,
// so only one of them runs at a time.
func stopRunningInstances() error {
	names, err := internal.RunningContainerNames()
	if err != nil {
		return fmt.Errorf("list running containers: %w", err)
	}

	for _, name := range names {
		if name != pg18Instance && !strings.HasPrefix(name, pg17Prefix) {
			continue
		}

		err := internal.StopContainerIfExists(name)
		if err != nil {
			return fmt.Errorf("stop the %q container: %w", name, err)
		}
	}

	return nil
}

// DB calls DBWithName using the current directory name as the database name.
func DB() error {
	cwd := internal.MustGetWD()
	name := filepath.Base(cwd)

	return DBWithName(name)
}

// DropDB calls DropDBWithName using the current directory name as the database name.
func DropDB() error {
	cwd := internal.MustGetWD()
	name := filepath.Base(cwd)

	return DropDBWithName(name)
}

// DBWithName creates a database and login role with the same name and the
// password 'pass'.
func DBWithName(name string) error {
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, "postgres://admin:pass@localhost")
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}

	_, err = conn.Exec(ctx, fmt.Sprintf(
		"CREATE ROLE %q WITH LOGIN PASSWORD 'pass'",
		name,
	))
	if err != nil {
		return fmt.Errorf("create login role: %w", err)
	}

	_, err = conn.Exec(ctx, fmt.Sprintf(
		"CREATE DATABASE %q WITH OWNER %q",
		name, name,
	))
	if err != nil {
		return fmt.Errorf("create database: %w", err)
	}

	return nil
}

// DropDBWithName drops the database and login role with the same name.
func DropDBWithName(name string) error {
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, "postgres://admin:pass@localhost")
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}

	_, err = conn.Exec(ctx, fmt.Sprintf(
		"DROP DATABASE %q", name,
	))
	if err != nil {
		return fmt.Errorf("drop database: %w", err)
	}

	_, err = conn.Exec(ctx, fmt.Sprintf(
		"DROP ROLE %q", name,
	))
	if err != nil {
		return fmt.Errorf("drop login role: %w", err)
	}

	return nil
}

// ConnString prints the connection string for use with psql like so:
//
//	psql $(mage sql:connstring)
func ConnString() error {
	_, _ = fmt.Fprintln(os.Stdout, MustGetConnString())

	return nil
}

func MustGetConnString() string {
	connString := os.Getenv("CONN_STRING")
	if connString == "" {
		cwd := internal.MustGetWD()
		name := filepath.Base(cwd)

		connString = fmt.Sprintf(
			"postgres://%s:pass@localhost/%s",
			name, name)
	}

	return connString
}

// GrantReporting grants read access to the provided tables. The user will be
// prompted for the reporting role and connection string.
func GrantReporting(ctx context.Context, tables []string) error {
	if len(tables) == 0 {
		return errors.New("no tables provided")
	}

	role, err := ia.PromptForValueWithDefault(
		"Reporting role", "elephant_reporting_user")
	if err != nil {
		return fmt.Errorf("get reporting role: %w", err)
	}

	connStr, err := ia.PromptForValue("Enter connection string", true)
	if err != nil {
		return fmt.Errorf("get connection string: %w", err)
	}

	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}

	quoted := make([]string, len(tables))

	for i := range tables {
		quoted[i] = quoteIdentifier(tables[i])
	}

	_, err = conn.Exec(ctx, fmt.Sprintf(
		"GRANT SELECT ON %s TO %s",
		strings.Join(quoted, ","),
		quoteIdentifier(role),
	))
	if err != nil {
		return fmt.Errorf("grant select: %w", err)
	}

	return nil
}

// GrantReportingFromJSON unmarshals a JSON array of table names and grants
// read access to them. The user will be prompted for the reporting role and
// connection string.
func GrantReportingFromJSON(ctx context.Context, data []byte) error {
	var tables []string

	err := json.Unmarshal(data, &tables)
	if err != nil {
		return fmt.Errorf("unmarshal reporting tables: %w", err)
	}

	return GrantReporting(ctx, tables)
}

func quoteIdentifier(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
