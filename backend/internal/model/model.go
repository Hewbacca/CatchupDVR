package model

import "time"

type Channel struct {
	ID     string `json:"id"`
	Number string `json:"number"`
	Name   string `json:"name"`
}

type Program struct {
	ID          string    `json:"id"`
	ChannelID   string    `json:"channelId"`
	Channel     Channel   `json:"channel"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Title       string    `json:"title"`
	Subtitle    string    `json:"subtitle,omitempty"`
	Description string    `json:"description,omitempty"`
	Category    string    `json:"category,omitempty"`
}

type Guide struct {
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Channels []Channel `json:"channels"`
	Programs []Program `json:"programs"`
}

type Recording struct {
	ID             int64      `json:"id"`
	ProgramID      string     `json:"programId"`
	ChannelID      string     `json:"channelId"`
	ChannelNumber  string     `json:"channelNumber"`
	Title          string     `json:"title"`
	ProgramStart   time.Time  `json:"programStart"`
	ProgramEnd     time.Time  `json:"programEnd"`
	ScheduledStart time.Time  `json:"scheduledStart"`
	ScheduledEnd   time.Time  `json:"scheduledEnd"`
	Status         string     `json:"status"`
	PlaylistPath   string     `json:"playlistPath,omitempty"`
	ProcessID      int        `json:"processId,omitempty"`
	StartedAt      *time.Time `json:"startedAt,omitempty"`
	FinishedAt     *time.Time `json:"finishedAt,omitempty"`
	HeartbeatAt    *time.Time `json:"heartbeatAt,omitempty"`
	ErrorMessage   string     `json:"errorMessage,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
}
