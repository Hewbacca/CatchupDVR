package guide

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Hewbacca/CatchupDVR/backend/internal/model"
)

const (
	publicChannelsURL   = "https://iptv-org.github.io/api/channels.json"
	publicLogosURL      = "https://iptv-org.github.io/api/logos.json"
	wikipediaSummaryURL = "https://en.wikipedia.org/api/rest_v1/page/summary/"
	maximumCatalogSize  = 24 << 20
	logoLookupUserAgent = "CatchUpDVR/1.16 (https://github.com/Hewbacca/CatchupDVR)"
)

var usCallsign = regexp.MustCompile(`^[KW][A-Z]{2,5}(?:-TV)?$`)

type publicChannel struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	AltNames []string `json:"alt_names"`
	Country  string   `json:"country"`
}

type publicLogo struct {
	Channel string  `json:"channel"`
	Feed    *string `json:"feed"`
	InUse   bool    `json:"in_use"`
	URL     string  `json:"url"`
}

type wikipediaSummary struct {
	Type      string `json:"type"`
	Extract   string `json:"extract"`
	Thumbnail struct {
		Source string `json:"source"`
	} `json:"thumbnail"`
}

type networkCandidate struct {
	Affiliation string
	Page        string
}

var knownNetworks = []networkCandidate{
	{Affiliation: "NBC", Page: "NBC"},
	{Affiliation: "ABC", Page: "American_Broadcasting_Company"},
	{Affiliation: "CBS", Page: "CBS"},
	{Affiliation: "Fox", Page: "Fox_Broadcasting_Company"},
	{Affiliation: "PBS", Page: "PBS"},
	{Affiliation: "The CW", Page: "The_CW"},
	{Affiliation: "MyNetworkTV", Page: "MyNetworkTV"},
	{Affiliation: "Ion Television", Page: "Ion_Television"},
	{Affiliation: "Telemundo", Page: "Telemundo"},
	{Affiliation: "Univision", Page: "Univision"},
	{Affiliation: "UniMás", Page: "UniMás"},
}

// ResolvePublicChannelLogos uses exact station-name or call-sign matches from
// iptv-org's public catalog. It deliberately avoids fuzzy matches: a blank
// channel label is preferable to showing the wrong network's identity.
//
// Callers persist every result, so this lookup is only needed for a station's
// first discovery (or a later retry after the catalog is unavailable).
func ResolvePublicChannelLogos(ctx context.Context, channels []model.Channel) map[string]string {
	if len(channels) == 0 {
		return nil
	}
	client := &http.Client{Timeout: 5 * time.Second}
	var catalog []publicChannel
	resolved := make(map[string]string)
	if downloadJSON(ctx, client, publicChannelsURL, &catalog) {
		candidates := make(map[string][]publicChannel)
		for _, station := range catalog {
			for _, name := range append([]string{station.Name}, station.AltNames...) {
				if key := normalizedStationName(name); key != "" {
					candidates[key] = append(candidates[key], station)
				}
			}
		}

		matched := make(map[string]publicChannel)
		for _, channel := range channels {
			matches := candidates[normalizedStationName(channel.Name)]
			if len(matches) == 0 {
				continue
			}
			best, found := chooseStation(matches)
			if found {
				matched[channel.ID] = best
			}
		}
		if len(matched) > 0 {
			var catalogLogos []publicLogo
			if downloadJSON(ctx, client, publicLogosURL, &catalogLogos) {
				logos := indexedLogos(catalogLogos)
				for channelID, station := range matched {
					if logo, ok := logos[station.ID]; ok {
						resolved[channelID] = logo.URL
					}
				}
			}
		}
	}
	for _, channel := range channels {
		if _, found := resolved[channel.ID]; !found {
			if logoURL := resolveAffiliateNetworkLogo(ctx, client, channel.Name); logoURL != "" {
				resolved[channel.ID] = logoURL
			}
		}
	}
	if len(resolved) == 0 {
		return nil
	}
	return resolved
}

func indexedLogos(catalogLogos []publicLogo) map[string]publicLogo {
	logos := make(map[string]publicLogo)
	for _, logo := range catalogLogos {
		if !logo.InUse || !IsSafeLogoURL(logo.URL) {
			continue
		}
		current, exists := logos[logo.Channel]
		if !exists || (current.Feed != nil && logo.Feed == nil) {
			logos[logo.Channel] = logo
		}
	}
	return logos
}

// resolveAffiliateNetworkLogo considers only conventional US broadcast call
// signs and an explicit affiliation phrase. This lets KUSA resolve to NBC,
// for example, without guessing from a station's city, channel number, or
// unrelated programmes mentioned in its description.
func resolveAffiliateNetworkLogo(ctx context.Context, client *http.Client, stationName string) string {
	stationName = strings.ToUpper(strings.TrimSpace(stationName))
	if !usCallsign.MatchString(stationName) {
		return ""
	}
	var station wikipediaSummary
	if !downloadJSON(ctx, client, wikipediaSummaryURL+url.PathEscape(stationName), &station) || station.Type != "standard" {
		return ""
	}
	networkPage := ""
	description := strings.ToLower(station.Extract)
	for _, network := range knownNetworks {
		if strings.Contains(description, "affiliated with "+strings.ToLower(network.Affiliation)) {
			networkPage = network.Page
			break
		}
	}
	if networkPage == "" {
		return ""
	}
	var network wikipediaSummary
	if !downloadJSON(ctx, client, wikipediaSummaryURL+url.PathEscape(networkPage), &network) || network.Type != "standard" || !IsSafeLogoURL(network.Thumbnail.Source) {
		return ""
	}
	return network.Thumbnail.Source
}

func chooseStation(matches []publicChannel) (publicChannel, bool) {
	for _, match := range matches {
		if strings.EqualFold(match.Country, "US") {
			return match, true
		}
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	return publicChannel{}, false
}

func downloadJSON(ctx context.Context, client *http.Client, location string, target any) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return false
	}
	request.Header.Set("User-Agent", logoLookupUserAgent)
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false
	}
	return json.NewDecoder(io.LimitReader(response.Body, maximumCatalogSize)).Decode(target) == nil
}

// IsSafeLogoURL accepts only ordinary HTTP(S) image locations. It prevents a
// guide file from injecting data:, file:, credential-bearing, or malformed URLs
// into the browser-facing channel metadata.
func IsSafeLogoURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" && parsed.User == nil
}

func normalizedStationName(value string) string {
	var result strings.Builder
	for _, character := range strings.ToUpper(strings.TrimSpace(value)) {
		if (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') {
			result.WriteRune(character)
		}
	}
	return result.String()
}
