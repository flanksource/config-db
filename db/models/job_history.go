package models

import (
	"os"
	"time"

	"github.com/flanksource/config-db/db/types"
)

type JobHistory struct {
	Name         string
	SuccessCount int
	ErrorCount   int
	Hostname     string
	TimeTakenMs  int64
	ResourceType string
	ResourceID   string
	Details      types.JSONMap
	TimeStart    time.Time `gorm:"-"`
	TimeEnd      time.Time `gorm:"-"`
	Errors       []string  `gorm:"-"`
}

type JobHistories []JobHistory

func (histories JobHistories) Prepare() JobHistories {
	var preparedHistories JobHistories
	for _, h := range histories {
		h.TimeTakenMs = h.TimeEnd.Sub(h.TimeStart).Milliseconds()
		h.Hostname, _ = os.Hostname()
		h.ResourceType = "config_item"
		h.Details = map[string]any{
			"errors": h.Errors,
		}
		preparedHistories = append(preparedHistories, h)
	}
	return preparedHistories
}
