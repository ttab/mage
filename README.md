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

### `twirp:generate` "name"

Generate runs protoc to compile the service declaration and generate an openapi3 specification.

## SQL tasks

### `sql:generate`

Generate uses sqlc to compile the SQL queries in postgres/queries.sql to Go, adding he default sqlc.yaml file if necessary.

### `sql:postgres` "name"

Postgres creates a local Postgres instance using docker. Data will be stored in "~/localstate/postgres-[name]".

### `sql:db`

DB calls DBWithName using the current directory name as the database name.

### `sql:dbwithname` "name"

Creates a local database and login role with the same name and the password 'pass'.

### `sql:migrate`

Migrate the database to the latest version using the migrations in "./schema".

### `sql:rollback` N

Rollback to a specific schema version:

``` shell
mage sql:rollback 1
```

### `sql:dumpschema`

DumpSchema writes the current database schema to "./postgres/schema.sql".

## S3 tasks

### `s3:minio`

Minio creates a local minio instance using docker. Data will be stored in "~/localstate/minio".

Exposes an S3 compatible endpoint on http://localhost:9000 and a web GUI on http://localhost:9001.

Use minioadmin/minioadmin to log in, or as access key/secret for the API.

### `s3:bucket` "name"

Creates a bucket in the local minio instance.
