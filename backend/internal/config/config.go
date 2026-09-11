package config

import (
	"os"
	"strconv"
)

type Config struct {
	HTTPAddr        string
	DatabasePath    string
	RecordingsDir   string
	WebDir          string
	HDHomeRunIP     string
	XMLTVFallback   string
	TunerCount      int
	FFmpegPath      string
	GPUMode         string
	GPURenderDevice string
	PrePadding      int
	PostPadding     int
	Deinterlace     bool
}

func FromEnv() Config {
	return Config{
		HTTPAddr:        env("HTTP_ADDR", ":8080"),
		DatabasePath:    env("DATABASE_PATH", "./data/catchup.db"),
		RecordingsDir:   env("RECORDINGS_DIR", "./recordings"),
		WebDir:          env("WEB_DIR", "../web/dist"),
		HDHomeRunIP:     os.Getenv("HDHOMERUN_IP"),
		XMLTVFallback:   os.Getenv("XMLTV_FALLBACK"),
		TunerCount:      envInt("TUNER_COUNT", 2),
		FFmpegPath:      env("FFMPEG_PATH", "ffmpeg"),
		GPUMode:         env("GPU_MODE", "auto"),
		GPURenderDevice: os.Getenv("GPU_RENDER_DEVICE"),
		PrePadding:      envInt("PRE_PADDING_MINUTES", 2),
		PostPadding:     envInt("POST_PADDING_MINUTES", 5),
		Deinterlace:     envBool("DEINTERLACE", true),
	}
}

func envBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return fallback
	}
	return value
}
