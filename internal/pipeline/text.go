// Package pipeline validates platform-extracted text. It has no file or process access.
package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"unicode/utf8"

	p "resume-agent-kernel/internal/protocol"
)

type Document struct {
	Text     string
	Checksum string
}

func ReadText(ctx context.Context, input p.ResumeTextV2) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}
	invalid := errors.New("invalid canonical resume text")
	if input.Status != "ready" || len(input.Pages) < 1 || len(input.Pages) > 100 ||
		strings.TrimSpace(input.ExtractorVersion) == "" || len(input.ExtractorVersion) > 128 {
		return Document{}, invalid
	}
	for _, hash := range []string{input.FileSHA256, input.TextSHA256} {
		decoded, err := hex.DecodeString(hash)
		if err != nil || len(decoded) != 32 || strings.ToLower(hash) != hash {
			return Document{}, invalid
		}
	}
	size := len(input.Pages) - 1
	for _, page := range input.Pages {
		if !utf8.ValidString(page) || strings.ContainsAny(page, "\r\f\x00") {
			return Document{}, invalid
		}
		size += len(page)
		if size > p.MaxTextBytes {
			return Document{}, errors.New("resume text byte limit exceeded")
		}
	}
	text := strings.Join(input.Pages, "\f")
	hash := sha256.Sum256([]byte(text))
	if strings.TrimSpace(text) == "" || hex.EncodeToString(hash[:]) != input.TextSHA256 {
		return Document{}, invalid
	}
	return Document{Text: text, Checksum: input.FileSHA256}, nil
}
