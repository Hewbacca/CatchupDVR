package recording

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Profile struct {
	FFmpegPath   string
	Mode         string
	RenderDevice string
	Deinterlace  bool
}

type Command struct {
	Path string   `json:"path"`
	Args []string `json:"args"`
}

func HDHomeRunStreamURL(host, virtualChannel string) (string, error) {
	host = strings.TrimRight(strings.TrimSpace(host), "/")
	virtualChannel = strings.TrimSpace(virtualChannel)
	if host == "" || virtualChannel == "" {
		return "", fmt.Errorf("HDHomeRun host and virtual channel are required")
	}
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "http://" + host
	}
	parsed, err := url.Parse(host)
	if err != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("invalid HDHomeRun host %q", host)
	}
	parsed.Path = "/auto/v" + url.PathEscape(virtualChannel)
	parsed.RawQuery = ""
	parsed.Fragment = ""
	if parsed.Port() == "" {
		parsed.Host = net.JoinHostPort(parsed.Hostname(), "5004")
	}
	return parsed.String(), nil
}

func BuildCommand(profile Profile, inputURL, outputDir string) Command {
	path := profile.FFmpegPath
	if path == "" {
		path = "ffmpeg"
	}
	args := []string{"-hide_banner", "-nostdin", "-y"}
	mode := profile.Mode
	if mode == "auto" && profile.RenderDevice != "" {
		mode = "vaapi"
	}
	switch mode {
	case "vaapi":
		args = append(args, "-hwaccel", "vaapi", "-hwaccel_device", profile.RenderDevice, "-hwaccel_output_format", "vaapi", "-i", inputURL)
		if profile.Deinterlace {
			args = append(args, "-vf", "deinterlace_vaapi")
		}
		args = append(args, "-c:v", "h264_vaapi", "-qp", "23")
	case "qsv":
		args = append(args, "-hwaccel", "qsv", "-qsv_device", profile.RenderDevice, "-hwaccel_output_format", "qsv", "-i", inputURL)
		if profile.Deinterlace {
			args = append(args, "-vf", "deinterlace_qsv")
		}
		args = append(args, "-c:v", "h264_qsv", "-global_quality", "23")
	default:
		args = append(args, "-i", inputURL)
		if profile.Deinterlace {
			args = append(args, "-vf", "bwdif")
		}
		args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-crf", "22")
	}
	args = append(args,
		"-force_key_frames", "expr:gte(t,n_forced*4)",
		"-c:a", "aac", "-b:a", "160k", "-ac", "2",
		"-f", "hls", "-hls_time", "4", "-hls_playlist_type", "event",
		"-hls_flags", "append_list+discont_start+independent_segments+program_date_time+temp_file",
		"-hls_segment_filename", filepath.Join(outputDir, "segment-%09d.ts"),
		filepath.Join(outputDir, "index.m3u8"),
	)
	return Command{Path: path, Args: args}
}

func DetectRenderDevice(driDir, sysClassDir, override string) (string, error) {
	if override != "" {
		if _, err := os.Stat(override); err != nil {
			return "", fmt.Errorf("configured render device: %w", err)
		}
		return override, nil
	}
	matches, err := filepath.Glob(filepath.Join(driDir, "renderD*"))
	if err != nil || len(matches) == 0 {
		return "", err
	}
	sort.Strings(matches)
	for _, device := range matches {
		vendorPath := filepath.Join(sysClassDir, filepath.Base(device), "device", "vendor")
		vendor, err := os.ReadFile(vendorPath)
		if err == nil && strings.EqualFold(strings.TrimSpace(string(vendor)), "0x8086") {
			return device, nil
		}
	}
	return matches[0], nil
}
