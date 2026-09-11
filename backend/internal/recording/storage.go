package recording

import (
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/Hewbacca/CatchupDVR/backend/internal/model"
)

var timeLocal = func() *time.Location { return time.Local }

func recordingFolderName(recording model.Recording) string {
	var slug strings.Builder
	lastHyphen := false
	for _, character := range strings.ToLower(recording.Title) {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			slug.WriteRune(character)
			lastHyphen = false
		} else if slug.Len() > 0 && !lastHyphen {
			slug.WriteByte('-')
			lastHyphen = true
		}
	}
	title := strings.Trim(slug.String(), "-")
	if title == "" {
		title = "recording"
	}
	airing := recording.ProgramStart.In(timeLocal()).Format("01-02-2006-0304pm")
	return title + "-" + airing
}

func recordingOutputDir(root string, recording model.Recording) string {
	if recording.PlaylistPath != "" {
		return filepath.Dir(filepath.Join(root, filepath.FromSlash(recording.PlaylistPath)))
	}
	return filepath.Join(root, recordingFolderName(recording))
}
