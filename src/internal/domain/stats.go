package domain

import "time"

// AlertStats aggregates alert notification volume over a time window.
type AlertStats struct {
	Since         time.Time    `json:"since"`
	Until         time.Time    `json:"until"`
	Days          int          `json:"days"`
	Environment   string       `json:"environment"`
	Total         int          `json:"total"`
	Firing        int          `json:"firing"`
	Resolved      int          `json:"resolved"`
	PerDay        float64      `json:"per_day"`
	UniqueAlerts  int          `json:"unique_alerts"`
	Top3Share     float64      `json:"top3_share"`
	Top8Share     float64      `json:"top8_share"`
	Top30Share    float64      `json:"top30_share"`
	TopAlerts     []AlertCount `json:"top_alerts"`
	ByEnvironment []EnvCount   `json:"by_environment"`
	Environments  []string     `json:"environments"`
}

// AlertCount is the notification volume of one alert name.
type AlertCount struct {
	Name         string   `json:"name"`
	Environments []string `json:"environments"`
	Count        int      `json:"count"`
	Firing       int      `json:"firing"`
	Resolved     int      `json:"resolved"`
	Share        float64  `json:"share"`
}

// EnvCount is the notification volume of one environment.
type EnvCount struct {
	Environment  string  `json:"environment"`
	Count        int     `json:"count"`
	Firing       int     `json:"firing"`
	Resolved     int     `json:"resolved"`
	UniqueAlerts int     `json:"unique_alerts"`
	Share        float64 `json:"share"`
	TopAlert     string  `json:"top_alert"`
}
