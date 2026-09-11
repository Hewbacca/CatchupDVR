package guide

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/Hewbacca/CatchupDVR/backend/internal/model"
)

var (
	virtualChannelOnly   = regexp.MustCompile(`^\d+(?:[._-]\d+)?$`)
	virtualChannelPrefix = regexp.MustCompile(`^(\d+(?:[._-]\d+)?)(?:\s|$)`)
)

type xmlTV struct {
	Channels   []xmlChannel `xml:"channel"`
	Programmes []xmlProgram `xml:"programme"`
}

type xmlChannel struct {
	ID           string    `xml:"id,attr"`
	DisplayNames []string  `xml:"display-name"`
	Icons        []xmlIcon `xml:"icon"`
}

type xmlIcon struct {
	Source string `xml:"src,attr"`
}

type xmlProgram struct {
	Channel     string   `xml:"channel,attr"`
	Start       string   `xml:"start,attr"`
	Stop        string   `xml:"stop,attr"`
	Title       string   `xml:"title"`
	Subtitle    string   `xml:"sub-title"`
	Description string   `xml:"desc"`
	Categories  []string `xml:"category"`
}

func ParseXMLTV(reader io.Reader) ([]model.Channel, []model.Program, error) {
	var document xmlTV
	if err := xml.NewDecoder(reader).Decode(&document); err != nil {
		return nil, nil, fmt.Errorf("decode XMLTV: %w", err)
	}

	channels := make([]model.Channel, 0, len(document.Channels))
	channelByID := make(map[string]model.Channel, len(document.Channels))
	for _, raw := range document.Channels {
		if raw.ID == "" {
			continue
		}
		number, name := channelNames(raw)
		channel := model.Channel{ID: raw.ID, Number: number, Name: name, LogoURL: channelIcon(raw)}
		channels = append(channels, channel)
		channelByID[channel.ID] = channel
	}

	programs := make([]model.Program, 0, len(document.Programmes))
	for _, raw := range document.Programmes {
		start, err := parseXMLTVTime(raw.Start)
		if err != nil {
			return nil, nil, fmt.Errorf("programme %q start: %w", raw.Title, err)
		}
		end, err := parseXMLTVTime(raw.Stop)
		if err != nil {
			return nil, nil, fmt.Errorf("programme %q stop: %w", raw.Title, err)
		}
		if !end.After(start) || raw.Channel == "" || strings.TrimSpace(raw.Title) == "" {
			continue
		}
		channel := channelByID[raw.Channel]
		programs = append(programs, model.Program{
			ID:          programID(raw.Channel, start, raw.Title),
			ChannelID:   raw.Channel,
			Channel:     channel,
			Start:       start.UTC(),
			End:         end.UTC(),
			Title:       strings.TrimSpace(raw.Title),
			Subtitle:    strings.TrimSpace(raw.Subtitle),
			Description: strings.TrimSpace(raw.Description),
			Category:    first(raw.Categories),
		})
	}
	return channels, programs, nil
}

func channelNames(raw xmlChannel) (string, string) {
	if len(raw.DisplayNames) == 0 {
		return raw.ID, raw.ID
	}
	names := make([]string, 0, len(raw.DisplayNames))
	for _, value := range raw.DisplayNames {
		if value = strings.TrimSpace(value); value != "" {
			names = append(names, value)
		}
	}
	if len(names) == 0 {
		return raw.ID, raw.ID
	}
	number := ""
	for _, value := range names {
		if virtualChannelOnly.MatchString(value) {
			number = normalizeVirtualChannel(value)
			break
		}
	}
	if number == "" {
		for _, value := range append(names, strings.TrimSpace(raw.ID)) {
			if match := virtualChannelPrefix.FindStringSubmatch(value); len(match) == 2 {
				number = normalizeVirtualChannel(match[1])
				break
			}
		}
	}
	if number == "" {
		number = names[0]
	}
	name := ""
	for _, value := range names {
		if !virtualChannelPrefix.MatchString(value) {
			name = value
			break
		}
	}
	if name == "" {
		for _, value := range names {
			if normalizeVirtualChannel(value) != number {
				name = value
				break
			}
		}
	}
	if name == "" {
		name = number
	}
	return number, name
}

func channelIcon(raw xmlChannel) string {
	for _, icon := range raw.Icons {
		if source := strings.TrimSpace(icon.Source); source != "" {
			return source
		}
	}
	return ""
}

func normalizeVirtualChannel(value string) string {
	return strings.NewReplacer("-", ".", "_", ".").Replace(strings.TrimSpace(value))
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

func parseXMLTVTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{"20060102150405 -0700", "200601021504 -0700", "20060102150405", "200601021504"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported time %q", value)
}

func programID(channel string, start time.Time, title string) string {
	sum := sha256.Sum256([]byte(channel + "\x00" + start.UTC().Format(time.RFC3339Nano) + "\x00" + title))
	return hex.EncodeToString(sum[:12])
}
