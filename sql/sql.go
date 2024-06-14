package sql

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/magefile/mage/sh"
	"github.com/ttab/mage/internal"
)

const (
	sqlTools      = "ghcr.io/ttab/elephant-sqltools:v0.1.3"
	postgresImage = "docker.io/pgvector/pgvector:pg16"
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
		return err
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

	ok, err := sh.Exec(nil, outFile, os.Stderr,
		"docker", "run", "--rm", "--network", "host",
		postgresImage,
		"pg_dump", connString,
		"--schema-only", "--no-owner", "--no-privileges",
	)
	if err != nil {
		return fmt.Errorf("run pg_dump: %w", err)
	}

	if !ok {
		return errors.New("failed to run pg_dump in docker")
	}

	return nil
}

// Postgres creates a local Postgres instance using docker.
func Postgres(name string) error {
	uid := os.Getuid()
	gid := os.Getgid()

	stateDir, err := internal.StateDir()
	if err != nil {
		return fmt.Errorf("get state directory path: %w", err)
	}

	instanceName := "postgres-" + name

	dataDir := filepath.Join(stateDir, instanceName)

	err = os.MkdirAll(dataDir, 0o700)
	if err != nil {
		return fmt.Errorf("create local state directory: %w", err)
	}

	err = internal.StopContainerIfExists(instanceName)
	if err != nil {
		return fmt.Errorf("stop existing container: %w", err)
	}

	err = sh.Run("docker", "run", "-d", "--rm",
		"--name", instanceName,
		"--user", fmt.Sprintf("%d:%d", uid, gid),
		"-e", "POSTGRES_USER=admin",
		"-e", "POSTGRES_PASSWORD=pass",
		"-e", "PGDATA=/var/lib/postgresql/data/pgdata",
		"-v", fmt.Sprintf("%s:/var/lib/postgresql/data", dataDir),
		"-p", "5432:5432",
		postgresImage,
		"-c", "wal_level=logical",
		"-c", "log_lock_waits=on",
	)
	if err != nil {
		return fmt.Errorf("start postgres: %w", err)
	}

	return nil
}

// DB calls DBWithName using the current directory name as the database name.
func DB() error {
	cwd := internal.MustGetWD()
	name := filepath.Base(cwd)

	return DBWithName(name)
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
