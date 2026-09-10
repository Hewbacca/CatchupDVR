package guide

import (
	"bytes"
	"context"
	"fmt"
)

type Importer interface {
	ReplaceGuide(context.Context, []byte) (int, int, error)
}

type RefreshService struct {
	Fetcher Fetcher
	Store   Importer
}

func (s RefreshService) Refresh(ctx context.Context, source, location string) (int, int, error) {
	var data []byte
	var err error
	switch source {
	case "hdhomerun":
		data, err = s.Fetcher.HDHomeRun(ctx, location)
	case "url":
		data, err = s.Fetcher.URL(ctx, location)
	case "file":
		data, err = s.Fetcher.File(location)
	default:
		return 0, 0, fmt.Errorf("unsupported guide source %q", source)
	}
	if err != nil {
		return 0, 0, err
	}
	_, _, err = ParseXMLTV(bytes.NewReader(data))
	if err != nil {
		return 0, 0, err
	}
	return s.Store.ReplaceGuide(ctx, data)
}
