## Setup

### Setup local db link as environment variable.

```bash
export DB_URL=postgres://<username>@localhost:5432/config
```

### Create `config` database.

```sql
create database config
```

### Scape config and serve

Starting the server will run the migrations and start scraping in background (The `default-schedule` configuration will run scraping every 60 minutes if configuration is not explicitly specified).

```bash
make build

./.bin/config-db serve
```

To explicitly run scraping with a particular configuration:

```bash
./.bin/config-db run <scrapper-config.yaml> -vvv
config-db serve
```

See `fixtures/` for example scraping configurations.

### Migrations

Commands `./bin/config-db serve` or `./bin/config-db run` would run the migrations.

Setup [goose](https://github.com/pressly/goose) for more options on migration. Goose commands need to be run from `db/migrations` directory.

```bash
GOOSE_DRIVER=postgres GOOSE_DBSTRING="user=postgres dbname=config sslmode=disable" goose down
```

## Adding a new scraper

1. Create a new file in `scrapers/` which implements the `api/v1/Scraper` interface
2. Add a reference to the scraper in `scrapers/common`
3. Create a configuration struct in `api/v1` and add it into the `api/v1/types/ConfigScraper` struct
4. Add a fixture in `fixtures/`
