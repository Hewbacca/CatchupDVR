package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestScheduleHonorsTwoTunerLimitWithPadding(t *testing.T) {
	database, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	xmlData, err := os.ReadFile("../../testdata/guide.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = database.ReplaceGuide(context.Background(), xmlData); err != nil {
		t.Fatal(err)
	}
	guide, err := database.Guide(context.Background(), time.Date(2026, 9, 10, 14, 0, 0, 0, time.UTC), time.Date(2026, 9, 10, 19, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, program := range guide.Programs {
		ids[program.Title] = program.ID
	}
	if _, err = database.Schedule(context.Background(), ids["College Football"], 2, 5, 2); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Schedule(context.Background(), ids["Baseball"], 2, 5, 2); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Schedule(context.Background(), ids["Classic Movie"], 2, 5, 2); !errors.Is(err, ErrTunerConflict) {
		t.Fatalf("got %v, want tuner conflict", err)
	}
}
