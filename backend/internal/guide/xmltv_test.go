package guide

import (
	"os"
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
