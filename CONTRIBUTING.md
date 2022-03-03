## Adding a new scraper

1. Create a new file in `scrapers/` which implements the `api/v1/Scraper` interface
2. Add a reference to the scraper in `scrapers/common`
3. Create a configuration struct in `api/v1` and add it into the `api/v1/types/ConfigScraper` struct
4. Add a fixture in `fixtures/`
