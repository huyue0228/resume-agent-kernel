package pipeline

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	p "resume-agent-kernel/internal/protocol"
)

type boundedDocumentOutput struct{ buffer bytes.Buffer }

func (b *boundedDocumentOutput) Bytes() []byte { return b.buffer.Bytes() }
func (b *boundedDocumentOutput) Write(data []byte) (int, error) {
	if b.buffer.Len()+len(data) > 1<<20 {
		return 0, errors.New("document text budget exceeded")
	}
	return b.buffer.Write(data)
}
func documentCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	var out boundedDocumentOutput
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &out
	err := cmd.Run()
	return out.Bytes(), err
}

type Document struct {
	Text     string
	Checksum string
	OCRPages int
}
type DocumentProvider interface {
	Read(context.Context, p.ArtifactRefV1, int) (Document, error)
}
type LocalDocuments struct {
	Root, SigningKey string
	OCR              func(context.Context, p.ArtifactRefV1, int) (Document, error)
}

// Read 接受短期签名的相对路径，固定引擎参数不对 Agent 开放。
func (d LocalDocuments) Read(ctx context.Context, a p.ArtifactRefV1, maxPages int) (Document, error) {
	out := Document{Checksum: a.Checksum}
	if a.InlineText != "" {
		// 仅供测试注入的 DocumentProvider 使用；生产入口不接受内联正文。
		return out, errors.New("inline document is disabled")
	}
	if a.MediaType != "application/pdf" || a.SizeBytes <= 0 || a.SizeBytes > 32<<20 || a.ExpiresAt < time.Now().Unix() || a.ExpiresAt > time.Now().Add(time.Hour).Unix() || d.Root == "" || d.SigningKey == "" {
		return out, errors.New("invalid document reference")
	}
	mac := hmac.New(sha256.New, []byte(d.SigningKey))
	fmt.Fprintf(mac, "%s\n%s\n%d\n%d", a.Path, a.Checksum, a.SizeBytes, a.ExpiresAt)
	signature, err := hex.DecodeString(a.Signature)
	if err != nil || !hmac.Equal(signature, mac.Sum(nil)) {
		return out, errors.New("invalid document signature")
	}
	if filepath.IsAbs(a.Path) || filepath.Clean(a.Path) != a.Path || strings.HasPrefix(a.Path, "..") {
		return out, errors.New("invalid document path")
	}
	root, err := os.OpenRoot(d.Root)
	if err != nil {
		return out, errors.New("document store unavailable")
	}
	defer root.Close()
	file, err := root.Open(a.Path)
	if err != nil {
		return out, errors.New("document unavailable")
	}
	defer file.Close()
	bytes, err := io.ReadAll(io.LimitReader(file, (32<<20)+1))
	if err != nil || int64(len(bytes)) != a.SizeBytes {
		return out, errors.New("document size mismatch")
	}
	hash := sha256.Sum256(bytes)
	if hex.EncodeToString(hash[:]) != a.Checksum {
		return out, errors.New("document checksum mismatch")
	}
	dir, err := os.MkdirTemp("", "resume-document-")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(dir)
	pdf := filepath.Join(dir, "input.pdf")
	if err = os.WriteFile(pdf, bytes, 0600); err != nil {
		return out, err
	}
	info, err := documentCommand(ctx, "pdfinfo", pdf)
	if err != nil {
		return out, errors.New("document damaged or encrypted")
	}
	pages := 0
	for _, line := range strings.Split(string(info), "\n") {
		if strings.HasPrefix(line, "Pages:") {
			pages, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Pages:")))
		}
		if strings.HasPrefix(line, "Encrypted:") && strings.Contains(line, "yes") {
			return out, errors.New("encrypted document")
		}
	}
	if pages < 1 || pages > maxPages {
		return out, errors.New("document page budget exceeded")
	}
	text, err := documentCommand(ctx, "pdftotext", "-layout", "-enc", "UTF-8", pdf, "-")
	if err == nil && len([]rune(strings.Join(strings.Fields(string(text)), ""))) >= 50 {
		out.Text = string(text)
		return out, nil
	}
	var all strings.Builder
	if d.OCR != nil {
		return d.OCR(ctx, a, maxPages)
	}
	for page := 1; page <= pages; page++ {
		prefix := filepath.Join(dir, "page")
		n := strconv.Itoa(page)
		if err := exec.CommandContext(ctx, "pdftoppm", "-f", n, "-l", n, "-r", "150", "-singlefile", "-png", pdf, prefix).Run(); err != nil {
			return out, errors.New("OCR rendering failed")
		}
		text, err := documentCommand(ctx, "tesseract", prefix+".png", "stdout", "-l", "chi_sim+eng")
		if err != nil {
			return out, errors.New("OCR failed")
		}
		if all.Len()+len(text) > 1<<20 {
			return out, errors.New("document text budget exceeded")
		}
		all.Write(text)
		all.WriteString("\f")
		out.OCRPages++
	}
	out.Text = all.String()
	if len(strings.Fields(out.Text)) == 0 {
		return out, errors.New("document has no readable content")
	}
	return out, nil
}
