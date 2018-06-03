# kitchen sync

Prototype-ready synchronization between code and databases.

Specify the contents of a Postgres Database using HCL, rather than migrations, making local testing much easier.

# Usage

This tool assumes that a Postgres DB exists, and has tables created. It is also assumed that each table has a column
called id which is used as the primary key for that table.

Data to insert is specified using the following syntax:

```
{{ table_name }} "{{ unique_identifier }}" {
    {{ column }} = "{{ value }}"
    {{ column }} = {{ value }}
    ...


    _defaults = {
        {{ column }} = {{ value }}
    }
}
```

For example, given a table created as follows:

```
CREATE TABLE users (
  id serial unique,
  name text,
  city text,
  points int
);
```

we can specify data cleanly in data.hcl like so:

```
users "me" {
    name = "James"
    city = "New York"

    _defaults = {
        points = 10
    }
}
```

Run the tool as follows:

`go run main.go -clean -reset -db {{ database_url }}`

# Reset vs Clean

By default, all fields that are _not_ nested under the _defaults stanza will be reset back to their specified values
each time the tool has run. In the example above, if I had updated my name in the database from "James" to "Jim" it
 would be reset to "James" when I next run the tool.

When the reset flag is supplied, the fields under _defaults are also reset. This makes _defaults useful for modelling
fields that you want to remain stateful during testing.

When the clean flag is supplied, the full contents of the database are dropped first. This gets you back to a known
good state and deletes any data that has been added without using kitchen sync.

# Functions

Kitchen sync supports several terraform-like functions.

## Loading a file into a field

The $file{} command will load the contents of the specified file into the database.

```
users "me" {
    name = "$file{file.txt}"
}
```

## Referencing another entity

The ${} command will create a reference to another entity, using the unique ID supplied. This assumes that the ID
column of that entity is the primary key in the database.

This makes it much easier to specify relationships between entities without needing to care about ID stability, or
needing to remember the ID for each entity.

Given two tables created as follows:

```
CREATE TABLE users (
  id serial unique,
  name text
);

CREATE TABLE pets (
  id serial unique,
  owner int references users(id)
)
```

we can create a relationship between a user and a pet like so:

```
users "me" {
    name = "James"
}

pets "dog" {
    owner = "${users.me}"
}
```