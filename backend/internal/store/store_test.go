package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hewbacca/CatchupDVR/backend/internal/model"
	_ "modernc.org/sqlite"
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

func TestClaimDueRecordingAndFinish(t *testing.T) {
	database, recording := scheduledFixture(t)
	defer database.Close()
	now := recording.ScheduledStart.Add(time.Minute)

	claimed, err := database.ClaimDueRecording(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil || claimed.ID != recording.ID || claimed.Status != "recording" {
		t.Fatalf("unexpected claimed recording: %#v", claimed)
	}
	if err := database.SetRecordingProcess(context.Background(), claimed.ID, "1/index.m3u8", 1234, now); err != nil {
		t.Fatal(err)
	}
	if err := database.TouchRecording(context.Background(), claimed.ID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := database.FinishRecording(context.Background(), claimed.ID, "completed", "", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	all, err := database.Recordings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Status != "completed" || all[0].PlaylistPath != "1/index.m3u8" || all[0].ProcessID != 0 || all[0].FinishedAt == nil {
		t.Fatalf("unexpected completed recording: %#v", all)
	}
}

func TestClaimMarksElapsedRecordingFailed(t *testing.T) {
	database, recording := scheduledFixture(t)
	defer database.Close()

	claimed, err := database.ClaimDueRecording(context.Background(), recording.ScheduledEnd.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if claimed != nil {
		t.Fatalf("expected no claim, got %#v", claimed)
	}
	all, err := database.Recordings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if all[0].Status != "failed" || all[0].ErrorMessage == "" {
		t.Fatalf("unexpected elapsed recording: %#v", all[0])
	}
}

func TestCancelAllowsRecordingDeletion(t *testing.T) {
	database, recording := scheduledFixture(t)
	defer database.Close()
	cancelled, err := database.CancelRecording(context.Background(), recording.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != "cancelled" {
		t.Fatalf("unexpected cancellation: %#v", cancelled)
	}
	if err := database.DeleteRecording(context.Background(), recording.ID); err != nil {
		t.Fatal(err)
	}
	all, err := database.Recordings(context.Background())
	if err != nil || len(all) != 0 {
		t.Fatalf("recording was not deleted: %#v, %v", all, err)
	}
}

func TestOpenMigratesExistingRecordingTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE recordings (
id INTEGER PRIMARY KEY AUTOINCREMENT, program_id TEXT NOT NULL, channel_id TEXT NOT NULL,
channel_number TEXT NOT NULL, title TEXT NOT NULL, program_start_unix INTEGER NOT NULL,
program_end_unix INTEGER NOT NULL, scheduled_start_unix INTEGER NOT NULL,
scheduled_end_unix INTEGER NOT NULL, status TEXT NOT NULL, playlist_path TEXT NOT NULL DEFAULT '',
created_at_unix INTEGER NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if _, err := upgraded.Recordings(context.Background()); err != nil {
		t.Fatalf("read upgraded schema: %v", err)
	}
}

func TestAuthenticationCredentialsPersistAndCannotBeOverwritten(t *testing.T) {
	database, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	credentials := model.AuthCredentials{Username: "alex", PasswordHash: "bcrypt-hash", SessionSecret: []byte("session-secret"), CreatedAt: time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)}
	if err := database.CreateAuthCredentials(context.Background(), credentials); err != nil {
		t.Fatal(err)
	}
	stored, configured, err := database.AuthCredentials(context.Background())
	if err != nil || !configured {
		t.Fatalf("expected configured account, got configured=%t err=%v", configured, err)
	}
	if stored.Username != credentials.Username || stored.PasswordHash != credentials.PasswordHash || string(stored.SessionSecret) != string(credentials.SessionSecret) || !stored.CreatedAt.Equal(credentials.CreatedAt) {
		t.Fatalf("credentials did not round-trip: %#v", stored)
	}
	if err := database.CreateAuthCredentials(context.Background(), credentials); !errors.Is(err, ErrAuthenticationConfigured) {
		t.Fatalf("expected one-time setup protection, got %v", err)
	}
}

func TestGuideRefreshCorrectsChannelNumberForScheduledJobs(t *testing.T) {
	database, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	badGuide := `<tv><channel id="station"><display-name>KCDODT</display-name></channel>
<programme start="20260910150000 +0000" stop="20260910160000 +0000" channel="station"><title>Test</title></programme></tv>`
	if _, _, err := database.ReplaceGuide(context.Background(), []byte(badGuide)); err != nil {
		t.Fatal(err)
	}
	guide, err := database.Guide(context.Background(), time.Date(2026, 9, 10, 14, 0, 0, 0, time.UTC), time.Date(2026, 9, 10, 17, 0, 0, 0, time.UTC))
	if err != nil || len(guide.Programs) != 1 {
		t.Fatalf("load guide: %v", err)
	}
	job, err := database.Schedule(context.Background(), guide.Programs[0].ID, 0, 0, 2)
	if err != nil || job.ChannelNumber != "KCDODT" {
		t.Fatalf("schedule original channel: %#v, %v", job, err)
	}
	correctedGuide := strings.Replace(badGuide, "<display-name>KCDODT</display-name>", "<display-name>KCDODT</display-name><display-name>3.1</display-name>", 1)
	if _, _, err := database.ReplaceGuide(context.Background(), []byte(correctedGuide)); err != nil {
		t.Fatal(err)
	}
	recordings, err := database.Recordings(context.Background())
	if err != nil || len(recordings) != 1 || recordings[0].ChannelNumber != "3.1" {
		t.Fatalf("scheduled channel was not corrected: %#v, %v", recordings, err)
	}
}

func scheduledFixture(t *testing.T) (*Store, model.Recording) {
	t.Helper()
	database, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	xmlData, err := os.ReadFile("../../testdata/guide.xml")
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, _, err := database.ReplaceGuide(context.Background(), xmlData); err != nil {
		database.Close()
		t.Fatal(err)
	}
	guide, err := database.Guide(context.Background(), time.Date(2026, 9, 10, 14, 0, 0, 0, time.UTC), time.Date(2026, 9, 10, 19, 0, 0, 0, time.UTC))
	if err != nil || len(guide.Programs) == 0 {
		database.Close()
		t.Fatalf("load fixture guide: %v", err)
	}
	recording, err := database.Schedule(context.Background(), guide.Programs[0].ID, 0, 0, 2)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	return database, recording
}
