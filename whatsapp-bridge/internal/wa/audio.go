package wa

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ConvertToOpus converts any audio file to an Ogg Opus voice note with
// ffmpeg. The caller must delete the returned file.
func ConvertToOpus(ctx context.Context, ffmpegPath, input string) (string, error) {
	bin := ffmpegPath
	if bin == "" {
		var err error
		bin, err = exec.LookPath("ffmpeg")
		if err != nil {
			return "", fmt.Errorf("ffmpeg is not installed (needed to turn %s into a voice note); install it or send an .ogg file", filepath.Ext(input))
		}
	}
	out, err := os.CreateTemp("", "wa-voice-*.ogg")
	if err != nil {
		return "", err
	}
	out.Close()
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, "-y", "-loglevel", "error", "-i", input,
		"-vn", "-c:a", "libopus", "-b:a", "32k", "-ar", "48000", "-ac", "1", "-application", "voip", out.Name())
	if b, err := cmd.CombinedOutput(); err != nil {
		os.Remove(out.Name())
		return "", fmt.Errorf("ffmpeg failed: %v %s", err, strings.TrimSpace(string(b)))
	}
	return out.Name(), nil
}
