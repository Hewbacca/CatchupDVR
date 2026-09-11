package guide

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseXMLTV(t *testing.T) {
	file, err := os.Open("../../testdata/guide.xml")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	channels, programs, err := ParseXMLTV(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 3 || len(programs) != 5 {
		t.Fatalf("got %d channels and %d programs", len(channels), len(programs))
	}
	if programs[0].Channel.Number != "7.1" || programs[0].Title != "College Football" {
		t.Fatalf("unexpected first program: %#v", programs[0])
	}
	want := time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC)
	if !programs[0].Start.Equal(want) {
		t.Fatalf("start %s, want %s", programs[0].Start, want)
	}
}

func TestParseXMLTVFindsChannelNumberWhenCallsignComesFirst(t *testing.T) {
	xml := `<tv>
<channel id="station-123"><display-name>KCDODT</display-name><display-name>3.1</display-name></channel>
<programme start="20260910090000 -0600" stop="20260910100000 -0600" channel="station-123"><title>Test</title></programme>
</tv>`
	channels, programs, err := ParseXMLTV(strings.NewReader(xml))
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 1 || channels[0].Number != "3.1" || channels[0].Name != "KCDODT" {
		t.Fatalf("unexpected channel: %#v", channels)
	}
	if len(programs) != 1 || programs[0].Channel.Number != "3.1" {
		t.Fatalf("unexpected program channel: %#v", programs)
	}
}

func TestChannelNamesAcceptsCombinedAndHyphenatedNumbers(t *testing.T) {
	number, name := channelNames(xmlChannel{ID: "station", DisplayNames: []string{"KCDODT", "3-1 KCDO", "3-1"}})
	if number != "3.1" || name != "KCDODT" {
		t.Fatalf("got number=%q name=%q", number, name)
	}
}
