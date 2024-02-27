# Mage tasks

Reusable mage tasks.

Import in your magefile to add the tasks:

``` go
//go:build mage
// +build mage

package main

import (
	//mage:import sql
	_ "github.com/ttab/mage/sql"
)
```

This will allow you to run the sql targets using: `mage sql:target-name`.

## `sql:postgres`

Postgres creates a local Postgres instance using docker.

## `sql:db`

DB creates a database and login role with the same name and the password 'pass'.

## `sql:migrate`

Migrate the database to the latest version using the migrations in "./schema".

## `sql:rollback`

Rollback to a specific schema version:

``` shell
mage sql:rollback 1
```

## `sql:dumpschema`

DumpSchema writes the current database schema to "./postgres/schema.sql".
