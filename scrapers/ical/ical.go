package ical

import (
	"bytes"
	"io"
	"time"

	"github.com/apognu/gocal"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/flanksource/confighub/httprequest"
)

type ICalScrapper struct{}

func (ical ICalScrapper) Scrape(ctx v1.ScrapeContext, configs v1.ConfigScraper, manager v1.Manager) (results v1.ScrapeResults) {
	config := configs.ICal
	var result = v1.ScrapeResult{
		BaseScraper: config.BaseScraper,
		Source:      config.URL,
	}

	data := fetchData(config.URL, manager.Requester)

	parsed, err := parse(bytes.NewBuffer(data))
	if err != nil {
		results = append(results, result.Errorf("failed to parse events: %s", err.Error()))
		return
	}

	events := make([]v1.Event, 0)
	for _, e := range parsed {
		events = append(events, transform(e))
	}

	results = append(results, result.Success(v1.ICalConfig{ChangeType: "ical", Events: events}))
	return

}

func fetchData(url string, requester httprequest.Requester) []byte {

	data, err := requester.Get(url)
	if err != nil {
		logger.Tracef("could not fetch data url(%s): %s", url, err.Error())
	}
	return data

}

func parse(r io.Reader) ([]gocal.Event, error) {

	// all events in 1 year
	start, end := time.Now(), time.Now().Add(12*30*24*time.Hour)

	c := gocal.NewParser(r)

	c.Start, c.End = &start, &end

	if err := c.Parse(); err != nil {
		return nil, err
	}

	return c.Events, nil

}

func transform(event gocal.Event) v1.Event {
	return v1.Event{
		Summary: event.Summary,
		Date:    event.Start.Format("January 2, 2006"),
	}
}
