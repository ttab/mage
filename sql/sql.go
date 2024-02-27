package sql

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/magefile/mage/sh"
)

const (
	sqlTools      = "ghcr.io/ttab/elephant-sqltools:v0.1.3"
	postgresImage = "docker.io/pgvector/pgvector:pg16"
)

func sqlcCommand() func(args ...string) error {
	uid := os.Getuid()
	gid := os.Getgid()
	cwd := mustGetWD()

	return sh.RunCmd("docker", "run", "--rm",
		"-v", fmt.Sprintf("%s:/usr/src", cwd),
		"-u", fmt.Sprintf("%d:%d", uid, gid),
		"--network", "host",
		sqlTools, "tern",
	)
}

func ternCommand() func(args ...string) error {
	cwd := mustGetWD()

	return sh.RunCmd("docker", "run", "--rm",
		"-v", fmt.Sprintf("%s:/usr/src", cwd),
		"--network", "host",
		sqlTools, "tern",
	)
}

func mustGetWD() string {
	cwd, err := os.Getwd()
	if err != nil {
		panic(fmt.Errorf("get current directory: %w", err))
	}

	return cwd
}

func mustGetConnString() string {
	connString := os.Getenv("CONN_STRING")
	if connString == "" {
		cwd := mustGetWD()
		name := filepath.Base(cwd)

		connString = fmt.Sprintf(
			"postgres://%s:pass@localhost/%s",
			name, name)
	}

	return connString
}

// Migrate the database to the latest version using the migrations in
// "./schema".
func Migrate() error {
	connString := mustGetConnString()
	tern := ternCommand()

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
	connString := mustGetConnString()
	tern := ternCommand()

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
	connString := mustGetConnString()

	outFile, err := os.Create(filepath.Join("postgres", "schema.sql"))
	if err != nil {
		return fmt.Errorf("create schema file: %w", err)
	}

	ok, err := sh.Exec(nil, outFile, os.Stderr,
		"docker", "run", "--rm", "--network", "host",
		postgresImage,
		"pg_dump", connString, "--schema-only",
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

	stateDir := os.Getenv("STATE_DIR")
	if stateDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("get home directory: %w", err)
		}

		stateDir = filepath.Join(homeDir, "localstate")
	}

	dataDir := filepath.Join(stateDir, "postgres-"+name)

	err := os.MkdirAll(dataDir, 0o700)
	if err != nil {
		return fmt.Errorf("create local state directory: %w", err)
	}

	err = sh.Run("docker", "run", "-d", "--rm",
		"--name", "postgres-"+name,
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

// DB creates a database and login role with the same name and the password
// 'pass'.
func DB(name string) error {
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
