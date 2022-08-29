package ical

import (
	"bytes"
	"io"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/apognu/gocal"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/confighub/api/v1"
)

const EventType = "event"

type ICalScrapper struct{}

type ICalConfig struct {
	URL string `json:"url,omitempty"`
}

func (ical ICalScrapper) Scrape(ctx v1.ScrapeContext, configs v1.ConfigScraper, manager v1.Manager) (results v1.ScrapeResults) {

	url, now := configs.ICal.URL, time.Now()

	icalConfig := ICalConfig{URL: url}

	var result = v1.ScrapeResult{
		LastModified: now,
		BaseScraper:  configs.ICal.BaseScraper,
		Config:       icalConfig,
	}

	data, err := fetch(url)
	if err != nil {
		results = append(results, result.Errorf("failed to fetch data from url(%s): %s", url, err.Error()))
		logger.Tracef("could not fetch data url(%s): %s", url, err.Error())
		return
	}

	parsed, err := parse(bytes.NewBuffer(data))
	if err != nil {
		results = append(results, result.Errorf("failed to parse events: %s", err.Error()))
		return
	}

	for _, e := range parsed {
		results = append(results, v1.ScrapeResult{
			LastModified: now,
			BaseScraper:  configs.ICal.BaseScraper,
			Config:       icalConfig,
			ChangeResult: eventToChangeResult(e),
		})
	}

	return

}

func fetch(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	client := http.Client{}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	defer res.Body.Close()

	data, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func parse(r io.Reader) ([]gocal.Event, error) {

	// all events in 1 year
	// TODO: unable to parse all the events if no start, end specified
	start, end := time.Now(), time.Now().Add(12*30*24*time.Hour)

	c := gocal.NewParser(r)

	c.Start, c.End = &start, &end

	if err := c.Parse(); err != nil {
		return nil, err
	}

	return c.Events, nil

}

func eventToChangeResult(event gocal.Event) *v1.ChangeResult {

	details := map[string]string{
		"description": event.Description,
		"location":    event.Location,
		"status":      event.Status,
	}
	changeResult := v1.ChangeResult{
		ChangeType: EventType,
		CreatedAt:  event.Created,
		Details:    details,
	}
	return &changeResult
}
