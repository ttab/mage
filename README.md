# Mage tasks

Reusable mage tasks.

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
)
```

This will allow you to run the sql targets using: `mage sql:target-name`.

## Twirp tasks

### `twirp:stub` "application" "Service" "MethodName"

Stub generates a protobuf service stub in `rpc/[application]/service.proto`.

### `twirp:generate` "name"

Generate runs protoc to compile the service declaration and generate an openapi3 specification.

## SQL tasks

### `sql:generate`

Generate uses sqlc to compile the SQL queries in postgres/queries.sql to Go, adding he default sqlc.yaml file if necessary.

### `sql:postgres`

Postgres creates a local Postgres instance using docker.

### `sql:db`

DB calls DBWithName using the current directory name as the database name.

### `sql:dbwithname` "name"

reates a local database and login role with the same name and the password 'pass'.

### `sql:migrate`

Migrate the database to the latest version using the migrations in "./schema".

### `sql:rollback` N

Rollback to a specific schema version:

``` shell
mage sql:rollback 1
```

### `sql:dumpschema`

DumpSchema writes the current database schema to "./postgres/schema.sql".
