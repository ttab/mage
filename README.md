# Mage tasks

Reusable [mage](https://magefile.org/) tasks.

Import in your "magefiles/magefile.go" to add the tasks:

``` go
//go:build mage
// +build mage

package main

import (
    //mage:import sql
    _ "github.com/ttab/mage/sql"
    //mage:import twirp
    _ "github.com/ttab/mage/twirp"
    //mage:import s3
    _ "github.com/ttab/mage/s3"
)
```

This will allow you to run the sql targets using: `mage sql:target-name`.

## Twirp tasks

### `twirp:stub` "application" "Service" "MethodName"

Stub generates a protobuf service stub in `rpc/[application]/service.proto`.

### `twirp:generate`

Generate auto-discovers all `rpc/*/service.proto` files, runs protoc to compile the service declarations, and generates openapi3 specifications. The version is resolved from the last ancestor git tag.

### `twirp:release` "version"

Release runs the same protoc compilation and openapi3 generation as `twirp:generate`, but uses the provided version string instead of resolving it from git tags.

## SQL tasks

### `sql:generate`

Generate uses sqlc to compile the SQL queries in postgres/queries.sql to Go, adding the default sqlc.yaml file if necessary.

### `sql:sqlcConfig`

SqlcConfig adds the default sqlc.yaml configuration file.

### `sql:postgres` "name"

Postgres creates a local Postgres instance using docker. Data will be stored under the platform data directory (e.g. `~/.local/share/tt-mage/postgres-[name]` on Linux, `~/Library/tt-mage/postgres-[name]` on macOS). Override with the `STATE_DIR` environment variable.

### `sql:db`

DB calls DBWithName using the current directory name as the database name.

### `sql:dbWithName` "name"

Creates a local database and login role with the same name and the password 'pass'.

### `sql:dropDB`

DropDB calls DropDBWithName using the current directory name as the database name.

### `sql:dropDBWithName` "name"

Drops the database and login role with the given name.

### `sql:migrate`

Migrate the database to the latest version using the migrations in "./schema".

### `sql:rollback` N

Rollback to a specific schema version:

``` shell
mage sql:rollback 1
```

### `sql:connString`

Prints the connection string for use with psql:

``` shell
psql $(mage sql:connString)
```

### `sql:dumpSchema`

DumpSchema writes the current database schema to "./postgres/schema.sql".

### `sql.GrantReporting`

GrantReporting is a reusable function (not a standalone target) that grants SELECT on the provided tables to a reporting role. It prompts interactively for the role name and connection string. Wrap it in your magefile to expose it as a target:

``` go
func GrantReporting(ctx context.Context) error {
    return sql.GrantReporting(ctx, []string{"my_table", "other_table"})
}
```

## S3 tasks

### `s3:minio`

Minio creates a local minio instance using docker. Data will be stored under the platform data directory (e.g. `~/.local/share/tt-mage/local-minio` on Linux, `~/Library/tt-mage/local-minio` on macOS).

Exposes an S3 compatible endpoint on http://localhost:9000 and a web GUI on http://localhost:9001.

Use minioadmin/minioadmin to log in, or as access key/secret for the API.

### `s3:bucket` "name"

Creates a bucket in the local minio instance.
