package browser

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
)

var (
	ErrInvalidURL          = errors.New("invalid URL")
	ErrUnsupportedPlatform = errors.New("unsupported platform")
)

// ValidateURL ensures raw is an absolute http(s) URL with a host and no credentials.
func ValidateURL(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, ErrInvalidURL
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return nil, ErrInvalidURL
		}
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, ErrInvalidURL
	}
	if u.Host == "" {
		return nil, ErrInvalidURL
	}
	if u.User != nil {
		return nil, ErrInvalidURL
	}

	return u, nil
}

// Open validates raw and opens it in the system default browser.
// Caller ctx can cancel work before the opener starts; the child process is not tied to ctx cancellation.
func Open(ctx context.Context, raw string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	u, err := ValidateURL(raw)
	if err != nil {
		return err
	}

	openURL := u.String()
	var cmd *exec.Cmd

	openerCtx := context.WithoutCancel(ctx)

	switch runtime.GOOS {
	case "darwin":
		//nolint:gosec // G204: openURL validated (http/https, host, no credentials) before exec.
		cmd = exec.CommandContext(openerCtx, "open", openURL)
	case "windows":
		//nolint:gosec // G204: openURL validated (http/https, host, no credentials) before exec.
		cmd = exec.CommandContext(openerCtx, "rundll32", "url.dll,FileProtocolHandler", openURL)
	case "linux", "freebsd", "openbsd":
		//nolint:gosec // G204: openURL validated (http/https, host, no credentials) before exec.
		cmd = exec.CommandContext(openerCtx, "xdg-open", openURL)
	default:
		return ErrUnsupportedPlatform
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	go func() {
		_ = cmd.Wait()
	}()

	return nil
}

// IsInvalidURL reports whether err is from ValidateURL or Open rejecting a URL.
func IsInvalidURL(err error) bool {
	return errors.Is(err, ErrInvalidURL)
}

// IsUnsupportedPlatform reports whether err is ErrUnsupportedPlatform.
func IsUnsupportedPlatform(err error) bool {
	return errors.Is(err, ErrUnsupportedPlatform)
}

// NormalizeURL returns the canonical string form of a validated URL.
func NormalizeURL(raw string) (string, error) {
	u, err := ValidateURL(raw)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(u.String()), nil
}
